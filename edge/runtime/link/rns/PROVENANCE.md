# Provenance of the Reticulum driver

This package is a Go port of parts of Reticulum **0.9.4**, the last release published under the MIT
licence. No code of any later Reticulum release was read, copied or ported.

## Source

| | |
|---|---|
| Repository | https://github.com/markqvist/Reticulum |
| Tag | `0.9.4` |
| Commit | `bfe5b876defa1de816b2f3fa0cc23ed0f0951d05` (2025-04-15) |
| Licence | `LICENSE` at that commit: "MIT License, unless otherwise noted", Copyright (c) 2016-2024 Mark Qvist / unsigned.io (full text below) |
| Ported on | 2026-10-31 |

Files read, and what was ported from them:

| 0.9.4 file | Header | Ported to |
|---|---|---|
| `RNS/Identity.py` | MIT | `identity.go` (keys, identity hash, sign/verify, encrypt/decrypt envelope), `destination.go` (announce validation) |
| `RNS/Destination.py` | MIT | `destination.go` (name expansion, name hash, destination hash, announce layout) |
| `RNS/Packet.py` | MIT | `packet.go` (header layout, flags, MTU/MDU, packet hash) |
| `RNS/Transport.py` | MIT | `transport.go` (hop counting, HEADER_2 via a transport node, path learning from announces, duplicate filter) |
| `RNS/Reticulum.py` | MIT | constants in `packet.go` (MTU, truncated hash length) |
| `RNS/Interfaces/TCPInterface.py` | MIT | `hdlc.go`, `tcp.go` (HDLC framing, client reconnect) |
| `RNS/Cryptography/Token.py` | MIT | `crypto.go` (token layout: IV, AES-128-CBC, HMAC-SHA256, key split) |
| `RNS/Cryptography/HKDF.py` | MIT | `crypto.go` (Go standard library `crypto/hkdf` used, same parameters) |
| `RNS/Interfaces/Interface.py` | MIT | nothing; read to explain announce ingress control in the interop test |

`RNS/Cryptography/HMAC.py` carries no MIT header and was not ported; the Go standard library is used.
All cryptography is the Go standard library (`crypto/ecdh` X25519, `crypto/ed25519`, `crypto/aes`,
`crypto/hmac`, `crypto/sha256`, `crypto/sha512`, `crypto/hkdf`). The package adds no dependency.

## Not used

Not read, copied or ported: Reticulum 0.9.5 and later (including current `master`), Quad4-Software
Reticulum-Go (Reticulum License), LXMF after its licence change, NomadNet, RNode firmware (GPL).

Newer Reticulum releases are only **run** as interop peers (`interop_test.go`, `RNS_VERSION=<version>`),
never read. Their behaviour is observed from the outside: packets on the wire and log output.

## Test vectors

`testdata/vectors_0.9.4.json` is produced by running the public API of `rns==0.9.4` from PyPI:

```
uv run --no-project --with rns==0.9.4 python testdata/gen_vectors.py > testdata/vectors_0.9.4.json
```

## Reticulum 0.9.4 licence

```
MIT License, unless otherwise noted

Copyright (c) 2016-2024 Mark Qvist / unsigned.io

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
