// Command keeper fetches each slot of fovea's signed observations from a
// macula station, keeps every new record that `fovea verify` accepts, logs
// every fetch with its time, and writes each slot's `fovea verify --chain`
// verdict (macula-fovea spec v0.5, 15-observations, "Keeping a history").
//
// One run is one fetch of every slot; the scheduled workflow runs it every
// 15 minutes and commits what changed.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/macula-io/macula-go/identity"
	"github.com/macula-io/macula-go/profile"
	"github.com/macula-io/macula-go/record"
	"github.com/macula-io/macula-go/stationlink"
	"github.com/macula-io/macula-go/transport"
)

// lateToleranceMs: a record dated more than 5 minutes before the keeper's
// previous fetch of its slot existed when the keeper last looked (spec 15).
const lateToleranceMs = 5 * 60 * 1000

type config struct {
	Realm        string    `json:"realm"`
	Profile      string    `json:"profile"`
	RealmKeyFile string    `json:"realm_key_file"`
	Stations     []station `json:"stations"`
	Slots        []slot    `json:"slots"`
}

type station struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
	Node string `json:"node"`
}

type slot struct {
	StorageKey     string `json:"storage_key"`
	AssessmentPath string `json:"assessment_path"`
}

func main() {
	cfgFile := flag.String("config", "keeper.json", "the keeper's configuration")
	root := flag.String("root", ".", "the records repository")
	fovea := flag.String("fovea", "fovea", "the released fovea binary")
	assessments := flag.String("assessments", "", "a clone of mcl-fovea-assessments")
	flag.Parse()
	if err := run(*cfgFile, *root, *fovea, *assessments); err != nil {
		fmt.Fprintln(os.Stderr, "keeper:", err)
		os.Exit(1)
	}
}

func run(cfgFile, root, fovea, assessments string) error {
	var cfg config
	b, err := os.ReadFile(cfgFile)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return fmt.Errorf("%s: %w", cfgFile, err)
	}
	p, err := profile.Parse(cfg.Profile)
	if err != nil {
		return err
	}
	link, err := dial(cfg.Stations, p)
	if err != nil {
		// The keeper's own outage goes into every slot's log: a gap it causes
		// must be visible as its own, not as the observer's.
		for _, s := range cfg.Slots {
			dir := filepath.Join(root, "records", s.StorageKey)
			if mkErr := os.MkdirAll(dir, 0o755); mkErr == nil {
				_ = logFetch(dir, time.Now().UnixMilli(), fmt.Sprintf("- keeper could not reach a station: %v", err))
			}
		}
		return err
	}
	defer link.Close("keeper run done")
	var failed []string
	for _, s := range cfg.Slots {
		if err := keepSlot(link, cfg, p, s, root, fovea, assessments); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", s.StorageKey, err))
		}
	}
	if len(failed) > 0 {
		return errors.New(strings.Join(failed, "; "))
	}
	return nil
}

// dial links to the first station that answers, pinned to its node id, with
// a throwaway identity: the keeper is nobody on the mesh.
func dial(stations []station, p profile.Profile) (*stationlink.Link, error) {
	key, err := identity.GenerateIdentityKey(p, identity.PuzzleDifficulty)
	if err != nil {
		return nil, err
	}
	issuer, err := identity.NewStatementIssuer(key, func() int64 { return time.Now().UnixMilli() })
	if err != nil {
		return nil, err
	}
	go issuer.Run(context.Background(), nil)
	var errs []string
	for _, s := range stations {
		ctx, cancel := context.WithTimeout(context.Background(), stationlink.HandshakeTimeout)
		link, err := stationlink.Dial(ctx, stationlink.Config{
			Target:      transport.Target{Host: s.Host, Port: s.Port, Profile: p, ExpectedNodeID: id32(s.Node)},
			IdentityKey: key, Issuer: issuer,
		})
		cancel()
		if err == nil {
			return link, nil
		}
		errs = append(errs, fmt.Sprintf("%s: %v", s.Host, err))
	}
	return nil, fmt.Errorf("no station answered: %s", strings.Join(errs, "; "))
}

func keepSlot(link *stationlink.Link, cfg config, p profile.Profile, s slot, root, fovea, assessments string) error {
	dir := filepath.Join(root, "records", s.StorageKey)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	previous := lastFetch(filepath.Join(dir, "fetches.log"))
	fetchedAt := time.Now().UnixMilli()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	v, err := link.FindRecord(ctx, id32(s.StorageKey))
	if err != nil {
		return logFetch(dir, fetchedAt, fmt.Sprintf("- fetch failed: %v", err))
	}
	r := v.Record()
	wire, err := record.Encode(r)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(wire)
	short := hex.EncodeToString(sum[:])[:16]
	if r.Type != 0x23 {
		// A tombstone or another type in the slot: kept apart from the chain.
		name := filepath.Join(dir, "other", fmt.Sprintf("type-%02x-%d-%s.hex", uint8(r.Type), r.CreatedAt, short))
		return keepFile(name, wire, dir, fetchedAt, fmt.Sprintf("%s type 0x%02x kept apart", short, uint8(r.Type)))
	}
	observer := identity.NodeIDOf(r.Key, p)
	endorsements, err := keepEndorsement(link, cfg.Realm, observer, root)
	if err != nil {
		return err
	}
	seq, chained := uintOf(r, "seq")
	name := fmt.Sprintf("unchained-%d-%s.hex", r.CreatedAt, short)
	if chained {
		name = fmt.Sprintf("%08d-%s.hex", seq, short)
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return logFetch(dir, fetchedAt, fmt.Sprintf("%s seen", short))
	}
	verdict, ok := verify(fovea, cfg, s, assessments, endorsements, wire)
	status := "kept"
	if previous > 0 && int64(r.CreatedAt) < previous-lateToleranceMs {
		status = "kept, PUBLISHED LATE (dated before the previous fetch)"
	}
	if !ok {
		path = filepath.Join(dir, "refused", name)
		status = "REFUSED by fovea verify: " + strings.ReplaceAll(strings.TrimSpace(verdict), "\n", " | ")
	}
	if err := keepFile(path, wire, dir, fetchedAt, fmt.Sprintf("%s seq %s created %s: %s", short, seqText(seq, chained),
		stamp(int64(r.CreatedAt)), status)); err != nil {
		return err
	}
	return writeChain(fovea, cfg, s, assessments, endorsements, dir)
}

// keepEndorsement fetches the observer's current realm member endorsement and
// keeps it if new; it returns every endorsement kept for the observer.
func keepEndorsement(link *stationlink.Link, realm string, observer [32]byte, root string) ([]string, error) {
	dir := filepath.Join(root, "endorsements", hex.EncodeToString(observer[:]))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	realmID := sha256.Sum256([]byte(realm))
	h := sha256.New()
	h.Write([]byte("MACULA-PQ-STORAGE-KEY-V1"))
	h.Write([]byte{0, byte(record.TypeRealmMemberEndorsement)})
	h.Write(realmID[:])
	h.Write(observer[:])
	var key [32]byte
	h.Sum(key[:0])
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if v, err := link.FindRecord(ctx, key); err == nil {
		r := v.Record()
		if wire, err := record.Encode(r); err == nil {
			sum := sha256.Sum256(wire)
			name := filepath.Join(dir, fmt.Sprintf("%d-%s.hex", r.CreatedAt, hex.EncodeToString(sum[:])[:16]))
			if _, err := os.Stat(name); err != nil {
				if err := os.WriteFile(name, []byte(hex.EncodeToString(wire)+"\n"), 0o644); err != nil {
					return nil, err
				}
			}
		}
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.hex"))
	sort.Strings(files)
	return files, err
}

func verify(fovea string, cfg config, s slot, assessments string, endorsements []string, wire []byte) (string, bool) {
	tmp, err := os.CreateTemp("", "record-*.hex")
	if err != nil {
		return err.Error(), false
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(hex.EncodeToString(wire) + "\n"); err != nil {
		return err.Error(), false
	}
	tmp.Close()
	out, err := exec.Command(fovea, append(verifyArgs(cfg, s, assessments, endorsements), tmp.Name())...).CombinedOutput()
	return string(out), err == nil
}

func writeChain(fovea string, cfg config, s slot, assessments string, endorsements []string, dir string) error {
	out, _ := exec.Command(fovea, append(verifyArgs(cfg, s, assessments, endorsements), "--chain", dir)...).CombinedOutput()
	return os.WriteFile(filepath.Join(dir, "chain.txt"), out, 0o644)
}

func verifyArgs(cfg config, s slot, assessments string, endorsements []string) []string {
	args := []string{"verify", "--realm-key", cfg.RealmKeyFile, "--realm", cfg.Realm, "--profile", cfg.Profile,
		"--repo", assessments, "--path", s.AssessmentPath}
	for _, e := range endorsements {
		args = append(args, "--endorsement", e)
	}
	return args
}

func keepFile(path string, wire []byte, dir string, fetchedAt int64, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(wire)+"\n"), 0o644); err != nil {
		return err
	}
	return logFetch(dir, fetchedAt, line)
}

// logFetch appends one line per fetch: when the keeper looked, and what it
// found. The log is the keeper's evidence of when a record was first there.
func logFetch(dir string, fetchedAt int64, line string) error {
	f, err := os.OpenFile(filepath.Join(dir, "fetches.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s %d %s\n", stamp(fetchedAt), fetchedAt, line)
	return err
}

// lastFetch is the time of the log's last successful fetch, 0 for none.
func lastFetch(log string) int64 {
	b, err := os.ReadFile(log)
	if err != nil {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var at int64
		var iso, rest string
		if n, _ := fmt.Sscanf(lines[i], "%s %d %s", &iso, &at, &rest); n == 3 && rest != "-" {
			return at
		}
	}
	return 0
}

func uintOf(r record.Record, key string) (uint64, bool) {
	v, present := r.Payload.Get(key)
	n, ok := v.AsInt64()
	return uint64(n), present && ok
}

func seqText(seq uint64, chained bool) string {
	if !chained {
		return "none (v0.4)"
	}
	return fmt.Sprint(seq)
}

func stamp(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339) }

func id32(s string) [32]byte {
	var out [32]byte
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		fmt.Fprintf(os.Stderr, "keeper: %q is not 64 hex digits\n", s)
		os.Exit(2)
	}
	copy(out[:], b)
	return out
}
