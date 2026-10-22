# mcl-fovea-records

*This exists so that anyone can check, at any time, that fovea's security verdicts about the Macula fleet were made continuously and not only when convenient.*

A public archive of the signed observations that [mcl-fovea](https://github.com/macula-services/mcl-fovea) makes of Macula stations, kept by a **keeper** ([mcl-fovea-keeper](https://github.com/macula-io/mcl-fovea-keeper)) that fetches them from the mesh every 15 minutes, verifies each one, and commits what it finds. Each observation is a macula record signed by the observer's node identity key, and from spec v0.5 each one names the hash of the one before it, so a gap, a duplicate history or a late record shows ([macula-fovea spec v0.5, 15-observations](https://github.com/macula-io/macula-fovea/blob/main/spec/v0.5/15-observations.md)).

## What is here

| Path | What |
| --- | --- |
| `records-wire1/<slot>/` | **The current era**: records signed on wire 1.0, from the 19 October 2026 flag day, kept and verified with `fovea` v0.5.0. Laid out exactly as `records/<slot>/` below. |
| `records/<slot>/` | **The frozen era before wire 1.0**, verified with `fovea` v0.4.0; the keeper no longer writes here. One directory per slot, named by its DHT storage key (the observer's signer key and the claim's subject). `NNNNNNNN-<hash>.hex`: a chained record, by seq; `unchained-<created_at>-<hash>.hex`: a spec v0.4 record, in no chain. Each file is the record's wire bytes in hex, exactly as the store returned them. |
| `records/<slot>/fetches.log` | Every fetch: when the keeper looked (UTC and milliseconds), and what it found (`seen`, `kept`, `PUBLISHED LATE`, `REFUSED`, or why it could not look). This is the keeper's evidence of when each record was first there. |
| `records/<slot>/chain.txt` | The latest `fovea verify --chain` verdict over the slot's records. |
| `records/<slot>/refused/`, `records/<slot>/other/` | Records `fovea verify` refused, and objects of another type found in the slot (a tombstone), kept apart from the chain. |
| `endorsements/<observer node id>/` | Every realm member endorsement of the observer the keeper has seen; a record is checked against the one that covered it. |
| `keeper.json` | The keeping policy: the realm, profile and realm key file, the stations the keeper asks, and the slots it keeps with the assessment each is verified against. [The keeper](https://github.com/macula-io/mcl-fovea-keeper) reads it from here on every run, and the `fovea verify` flags for any slot come from it. Changed only by a reviewed commit. |
| `realm/io.macula.pub.hex` | The io.macula realm's public key, the trust anchor, as published at `https://realm.macula.io/.well-known/macula-realm.json` (SHA-256 of the hex text: `99e4f08c1aa30405c1eb1e0e81649b2686f3964fae8ffe827f8107bc7777b78d`). |

## Eras, and the flag-day gap

A record is judged by the `fovea` that reads its wire format, and no `fovea` reads two. On 19 October 2026 the mesh moved to wire 1.0 and the record encoding moved with it, so the records are kept in two eras:

| Era | Directory | Verify with |
| --- | --- | --- |
| Before wire 1.0, up to 19 October 2026 | `records/<slot>/` (frozen, its `chain.txt` is the last verdict it got) | `fovea` v0.4.0 |
| Wire 1.0, from 19 October 2026 | `records-wire1/<slot>/` | `fovea` v0.5.0 |

**Between the two there is a gap, and it is ours.** From 2026-10-19T18:07Z the keeper, still on the old wire, could not link to any station (every slot's `fetches.log` says `keeper could not reach a station: ... unsupported frame version`) until it was rebuilt on wire 1.0. The observer kept signing through those days, but the DHT holds only each slot's latest record, so every record signed in between was replaced before anyone kept it. Those records are not here and cannot be recovered. Per slot: the last record the old era kept, and the first seq that is missing; the gap ends at the seq before the first file in `records-wire1/<slot>/`.

| Slot | Last kept (`records/`) | First missing |
| --- | --- | --- |
| `5af9a0dbf82aa982…` | 324 | 325 |
| `641abba37501f662…` | 324 | 325 |
| `6787ad3f4ffa398e…` | 331 | 332 |
| `69f3eba1b1a19d42…` | 331 | 332 |
| `6da7f2e6f79c07ac…` | 324 | 325 |
| `7b89fd8a7456fefb…` | 324 | 325 |
| `92126a7564541d1b…` | 324 | 325 |
| `acd7b32f8883f754…` | 331 | 332 |
| `c162c748e2e4006f…` | 331 | 332 |
| `cd256e6f6528cb46…` | 466 | 467 |
| `ddbb3f9e8a8c7e59…` | 331 | 332 |
| `fbcbeb8f2a9e06d8…` | 324 | 325 |

The first record in each `records-wire1/<slot>/` chain therefore names a predecessor that is not kept here, and `fovea verify --chain` reports that, as it should.

## Check it yourself

Nothing here needs to be trusted: every record verifies offline against the realm key and the assessment revision it names.

```sh
go install github.com/macula-io/macula-fovea/cli/cmd/fovea@v0.5.0
git clone https://github.com/macula-io/mcl-fovea-assessments.git
git clone https://github.com/macula-io/mcl-fovea-records.git && cd mcl-fovea-records
slot=records-wire1/cd256e6f6528cb46381ed5cfde72ca396de902f3680bcfaf9c19251f1b11ae0a
fovea verify --realm-key realm/io.macula.pub.hex --realm io.macula --profile pq_hybrid \
  $(for e in endorsements/*/*.hex; do printf -- '--endorsement %s ' "$e"; done) \
  --repo ../mcl-fovea-assessments --path macula-station-kx --chain "$slot"
```

For the frozen era, install `fovea@v0.4.0` and point `slot` at `records/<slot>` instead.

`continuous` means: every record verifies, they link one to the next with nothing missing, forked or out of order, each was signed within 5 minutes of its round and at most one cadence after the one before. It does not prove that the observer's clock is true, nor anything a single observation does not prove; spec 15 says exactly what it proves.

## Honest limits

- **The first keeper runs on our own box.** It is a systemd timer on a Macula lab machine: off the observer's own box and off every station it observes, but **not independent of us**. Independent keepers, run by others, are what make a withheld record visible to everyone; anyone can run [the keeper](https://github.com/macula-io/mcl-fovea-keeper) (a signed image, released `fovea` inside) and compare.
- **A keeper that fetches less often than the cadence loses records**, and the gaps it then shows are its own. Its own outages are logged in `fetches.log` as `keeper could not reach a station`.
- **Disclosure.** Every state is published as observed, `broken` included. With this keeper fetching every 15 minutes, **a broken claim becomes public here within about 15 minutes** of the round that saw it. That is the point.

## The keeper's machine

This repository holds data and the keeping policy (`keeper.json`), nothing that is ever executed. The keeper is a signed image from [mcl-fovea-keeper](https://github.com/macula-io/mcl-fovea-keeper), which the machine runs by digest as an unprivileged user, under a CPU and memory ceiling, on its own podman network: an IPv6 route to the stations, and none to the machine's own localhost-only services. It pushes with a deploy key that can write only this repository; that key was generated on the machine and never leaves it. A failed run fails the unit and is logged.
