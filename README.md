# mcl-fovea-records

*This exists so that anyone can check, at any time, that fovea's security verdicts about the Macula fleet were made continuously and not only when convenient.*

A public archive of the signed observations that [mcl-fovea](https://github.com/macula-services/mcl-fovea) makes of Macula stations, kept by a **keeper** ([mcl-fovea-keeper](https://github.com/macula-io/mcl-fovea-keeper)) that fetches them from the mesh every 15 minutes, verifies each one, and commits what it finds. Each observation is a macula record signed by the observer's node identity key, and from spec v0.5 each one names the hash of the one before it, so a gap, a duplicate history or a late record shows ([macula-fovea spec v0.5, 15-observations](https://github.com/macula-io/macula-fovea/blob/main/spec/v0.5/15-observations.md)).

## What is here

| Path | What |
| --- | --- |
| `records/<slot>/` | One directory per slot, named by its DHT storage key (the observer's signer key and the claim's subject). `NNNNNNNN-<hash>.hex`: a chained record, by seq; `unchained-<created_at>-<hash>.hex`: a spec v0.4 record, in no chain. Each file is the record's wire bytes in hex, exactly as the store returned them. |
| `records/<slot>/fetches.log` | Every fetch: when the keeper looked (UTC and milliseconds), and what it found (`seen`, `kept`, `PUBLISHED LATE`, `REFUSED`, or why it could not look). This is the keeper's evidence of when each record was first there. |
| `records/<slot>/chain.txt` | The latest `fovea verify --chain` verdict over the slot's records. |
| `records/<slot>/refused/`, `records/<slot>/other/` | Records `fovea verify` refused, and objects of another type found in the slot (a tombstone), kept apart from the chain. |
| `endorsements/<observer node id>/` | Every realm member endorsement of the observer the keeper has seen; a record is checked against the one that covered it. |
| `keeper.json` | The keeping policy: the realm, profile and realm key file, the stations the keeper asks, and the slots it keeps with the assessment each is verified against. [The keeper](https://github.com/macula-io/mcl-fovea-keeper) reads it from here on every run, and the `fovea verify` flags for any slot come from it. Changed only by a reviewed commit. |
| `realm/io.macula.pub.hex` | The io.macula realm's public key, the trust anchor, as published at `https://realm.macula.io/.well-known/macula-realm.json` (SHA-256 of the hex text: `99e4f08c1aa30405c1eb1e0e81649b2686f3964fae8ffe827f8107bc7777b78d`). |

## Check it yourself

Nothing here needs to be trusted: every record verifies offline against the realm key and the assessment revision it names.

```sh
go install github.com/macula-io/macula-fovea/cli/cmd/fovea@v0.3.0
git clone https://github.com/macula-io/mcl-fovea-assessments.git
git clone https://github.com/macula-io/mcl-fovea-records.git && cd mcl-fovea-records
slot=records/cd256e6f6528cb46381ed5cfde72ca396de902f3680bcfaf9c19251f1b11ae0a
fovea verify --realm-key realm/io.macula.pub.hex --realm io.macula --profile pq_hybrid \
  $(for e in endorsements/*/*.hex; do printf -- '--endorsement %s ' "$e"; done) \
  --repo ../mcl-fovea-assessments --path macula-station-kx --chain "$slot"
```

`continuous` means: every record verifies, they link one to the next with nothing missing, forked or out of order, each was signed within 5 minutes of its round and at most one cadence after the one before. It does not prove that the observer's clock is true, nor anything a single observation does not prove; spec 15 says exactly what it proves.

## Honest limits

- **The first keeper runs on our own box.** It is a systemd timer on a Macula lab machine: off the observer's own box and off every station it observes, but **not independent of us**. Independent keepers, run by others, are what make a withheld record visible to everyone; anyone can run [the keeper](https://github.com/macula-io/mcl-fovea-keeper) (a signed image, released `fovea` inside) and compare.
- **A keeper that fetches less often than the cadence loses records**, and the gaps it then shows are its own. Its own outages are logged in `fetches.log` as `keeper could not reach a station`.
- **Disclosure.** Every state is published as observed, `broken` included. With this keeper fetching every 15 minutes, **a broken claim becomes public here within about 15 minutes** of the round that saw it. That is the point.

## The keeper's machine

This repository holds data and the keeping policy (`keeper.json`), nothing that is ever executed. The keeper is a signed image from [mcl-fovea-keeper](https://github.com/macula-io/mcl-fovea-keeper), which the machine runs by digest as an unprivileged user, under a CPU and memory ceiling, on its own podman network: an IPv6 route to the stations, and none to the machine's own localhost-only services. It pushes with a deploy key that can write only this repository; that key was generated on the machine and never leaves it. A failed run fails the unit and is logged.
