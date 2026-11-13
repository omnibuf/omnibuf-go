# OmniBuf Conformance Runner & Adapter Guide

`omnibuf-go` hosts the canonical cross-language conformance suite and runner binary (`cmd/conformance-runner`). Every OmniBuf implementation validates wire-format compliance by providing an adapter executable that communicates with `conformance-runner` over standard input and output.

## 1. Running the Suite

Install `conformance-runner` from a release archive or build it with Go:

```bash
go install github.com/omnibuf/omnibuf-go/cmd/conformance-runner@latest
```

Pass the target adapter command and any adapter arguments:

```bash
# Run all 4 tiers (584 test cases)
conformance-runner ./bin/conformance-go

# Run against an interpreted adapter (such as Node.js or Python)
conformance-runner node ./packages/omnibuf-node/dist/bin/conformance-es.js

# Filter to specific implementation tiers during bring-up
conformance-runner --tier=blind,struct ./target/release/conformance-rs

# Emit machine-readable JSON results
conformance-runner --tier=blind --json ./target/release/conformance-rs
```

### Supported `--tier` Values

| Flag Token | Enum Value | Constant | Purpose |
| :--- | :--- | :--- | :--- |
| `plugin` | `1` | `TIER_PLUGIN` | Generated code from `protoc-gen-omnibuf-<lang>` including lazy `Opaque<T>` views and splicing |
| `proto` | `2` | `TIER_PROTOBUF_TYPES` | Direct serialization of standard `protoc` generated message types |
| `struct` | `3` | `TIER_ANNOTATED_STRUCT` | Native structs, classes, or records with field annotations or schema definitions |
| `blind` | `4` | `TIER_BLIND` | Schema-less packet decoding (`DecodeBlind`) with and without `Field #0` metadata |

---

## 2. Framing Protocol over `stdin` / `stdout`

`conformance-runner` spawns the adapter process once and streams all test cases over `stdin` and `stdout` until `EOF`.

Every frame in both directions consists of:
1. A **4-byte little-endian `uint32` byte length** (`0` to `16,777,216` bytes; frames larger than `16 MiB` are rejected).
2. An **OmniBuf-encoded payload** of that exact byte length (`ConformanceRequest` on `stdin`, `ConformanceResponse` on `stdout`).

Clean `EOF` on the 4-byte length read signals that all test cases have completed and the adapter process should exit with status `0`.

### `ConformanceRequest` Schema

| Field # | Name | OmniBuf Wire Type | Go / Proto Type | Description |
| :--- | :--- | :--- | :--- | :--- |
| `1` | `test_name` | `WireString` (`10`) | `string` | Unique test vector identifier |
| `2` | `message_type` | `WireString` (`10`) | `string` | Fully qualified message name (`omnibuf.bench.PeerUpdate`, `omnibuf.conformance.TestAllTypes`, `omnibuf.conformance.TreeNode`, or `omnibuf.conformance.NestedCluster`; `TIER_BLIND` passes the vector schema label) |
| `3` | `tier` | `WireUint` (`0`) | `uint32` | Target `TestTier` (`1..4`) |
| `4` | `requested_output_format` | `WireUint` (`0`) | `uint32` | `1` (`WireFormatOmniBuf`) or `2` (`WireFormatJSON`) |
| `5` | `omnibuf_payload` | `WireBytes` (`11`) | `[]byte` | Input wire packet to decode |
| `6` | `json_payload` | `WireString` (`10`) | `string` | Reserved for JSON-to-OmniBuf vectors |
| `7` | `self_describing` | `WireBoolFalse`/`True` (`2`/`3`) | `bool` | Re-encode output packet with `Field #0` (`SchemaMeta`) embedded |
| `8` | `test_opaque_splice` | `WireBoolFalse`/`True` (`2`/`3`) | `bool` | Apply the `OpaqueSpliceGrow` mutations via `Opaque<T>` setters and return the spliced packet |
| `9` | `test_opaque_splice_shrink` | `WireBoolFalse`/`True` (`2`/`3`) | `bool` | Apply the `OpaqueSpliceShrink` mutations via `Opaque<T>` setters and return the spliced packet |

### `ConformanceResponse` Schema

Exactly one result branch should be populated per response:

| Field # | Name | OmniBuf Wire Type | Go / Proto Type | Description |
| :--- | :--- | :--- | :--- | :--- |
| `1` | `parse_error` | `WireString` (`10`) | `string` | Non-empty when input bytes fail validation or wire decoding (expected on invalid vectors) |
| `2` | `serialize_error` | `WireString` (`10`) | `string` | Non-empty when re-encoding fails |
| `3` | `runtime_error` | `WireString` (`10`) | `string` | Unexpected panic, crash, or invariant failure |
| `4` | `skipped` | `WireString` (`10`) | `string` | Reason string if a test case is intentionally skipped |
| `5` | `omnibuf_payload` | `WireBytes` (`11`) | `[]byte` | Re-encoded or spliced OmniBuf wire bytes (when `requested_output_format == 1`) |
| `6` | `json_payload` | `WireString` (`10`) | `string` | Canonical blind-decoded JSON string (when `requested_output_format == 2`) |

---

## 3. Required Adapter Behavior by Tier

### `TIER_BLIND` (`tier = 4`)
1. Decode `omnibuf_payload` using schema-less `DecodeBlind`.
2. Return `parse_error` if the packet violates any wire invariant.
3. When `requested_output_format == 2` (`WireFormatJSON`), serialize the decoded map to JSON in `json_payload`. Packets containing `Field #0` must resolve declared field names across nested messages, arrays of sub-messages, and map values up to the recursion limit.

### `TIER_ANNOTATED_STRUCT` (`tier = 3`) & `TIER_PROTOBUF_TYPES` (`tier = 2`)
1. Unmarshal `omnibuf_payload` into a pre-populated destination object (pre-populating fields verifies that unmarshaling resets absent fields and clears stale sub-message pointers).
2. Return `parse_error` if decoding fails.
3. Re-marshal the decoded message to OmniBuf bytes (with deterministic map key ordering and `SelfDescribing: req.SelfDescribing`) and return the bytes in `omnibuf_payload`.

### `TIER_PLUGIN` (`tier = 1`)
1. Unmarshal `omnibuf_payload` into a pre-populated generated struct/object and verify that every lazy `Opaque<T>` getter returns the exact same field values as the eagerly decoded message.
2. Verify that `SpliceFields` preserves an aliased prefix slice (`dst[:prefixLen]`) and never mutates the underlying source packet buffer.
3. When `test_opaque_splice` (`OpaqueSpliceGrow`) is `true`, mutate the `Opaque` view with the exact grow values below and return `op.MarshalAppend(nil)` in `omnibuf_payload`:
   - **`omnibuf.bench.PeerUpdate` (Grow):**
     - `asn = 1_000_000`
     - `router_id = "edge-rtr-fra01-".repeat(20)`
     - `active = !op.GetActive()`
     - `uptime_sec = 18446744073709551615` (`uint64` max)
     - `rssi_dbm = -1_000_000`
     - `cpu_load = 99.875`
     - `tags = ["tag-grow-a", "tag-grow-b", "long-tag-".repeat(20)]`
     - `prefixes = [100000, 200000, 300000, 400000, 500000, 600000]`
     - `location = { region: "eu-central-grown", datacenter: "fra-dc-".repeat(25), city: "Berlin", rack: 999999, latitude: 52.52, longitude: 13.405 }`
     - `sequence = op.GetSequence() + 1`
   - **`omnibuf.conformance.TestAllTypes` (Grow):**
     - `f1_uint32 = 1_000_000`, `f2_uint64 = uint64_max`, `f3_sint32 = -1_000_000`, `f4_sint64 = int64_min`
     - `f5_bool = !op.GetF5Bool()`
     - `f6_string = "grown-str-".repeat(30)`
     - `f7_bytes = [0xab, 0xcd].repeat(130)` (260 bytes)
     - `f8_sub_msg = { id: 888888, label: "sub-grown-".repeat(20), flag: true, score: -999999999 }`
     - `f9_string_arr = ["arr-grow-1", "arr-grow-2", "arr-grow-3-".repeat(15)]`
     - `f10_uint32_arr = [100000, 200000, 300000, 400000, 500000]`
     - `f11_sub_msg_arr = [{ id: 100001, label: "elem0-".repeat(25), flag: true, score: -500000 }, { id: 100002, label: "elem1-grown", flag: false, score: 500000 }]`
     - `f12_map_str_uint = { "k1": 1000000, "k2": uint64_max, "k3_long_key": 424242 }`
     - `f13_float32 = -1234.5`, `f14_float64 = 3.141592653589793`
     - `f15_fixed32 = 0xdeadbeef`, `f16_fixed64 = 0xcafebabedeadbeef`
     - `f20_overflow_str = "overflow20-".repeat(20)`
4. When `test_opaque_splice_shrink` (`OpaqueSpliceShrink`) is `true`, mutate the `Opaque` view with the shrink values below and return `op.MarshalAppend(nil)`:
   - **`omnibuf.bench.PeerUpdate` (Shrink):**
     - `asn = 1`, `router_id = "r"`, `active = false`, `uptime_sec = 5`, `rssi_dbm = -1`, `cpu_load = 0.5`
     - `tags = ["t"]`, `prefixes = [1]`
     - `location = { region: "r", datacenter: "d", city: "c", rack: 1, latitude: 1.0, longitude: 2.0 }`
     - `sequence = 2`
   - **`omnibuf.conformance.TestAllTypes` (Shrink):**
     - `f1_uint32 = 1`, `f2_uint64 = 2`, `f3_sint32 = -1`, `f4_sint64 = -2`, `f5_bool = false`
     - `f6_string = "s"`, `f7_bytes = [0x01]`
     - `f8_sub_msg = { id: 1, label: "x", flag: false, score: -1 }`
     - `f9_string_arr = ["a"]`, `f10_uint32_arr = [7]`
     - `f11_sub_msg_arr = [{ id: 1, label: "e", flag: true, score: 1 }]`
     - `f12_map_str_uint = { "k": 1 }`
     - `f13_float32 = 0.5`, `f14_float64 = -0.25`, `f15_fixed32 = 7`, `f16_fixed64 = 9`, `f20_overflow_str = "z"`

---

## 4. Security & Validation Invariants

Every implementation must enforce the following checks before allocating buffers or returning decoded values:

1. **Prefix-Varint 9-Byte Marker Validation:** A 9-byte Prefix-Varint starts with `0x00` (`TZCNT == 8`). Any first byte with low 3 bits `000` and non-zero upper 5 bits (`b & 0x07 == 0 && b != 0`) is reserved and must fail with a parse error.
2. **Container Bounds Checking Before Allocation:** `WireString`, `WireBytes`, `WireSubMsg`, `WireArray`, `WireMap`, and `WireExtended` carry a `ByteLen` Prefix-Varint. Decoders must verify `pos + byteLen <= len(buf)` before allocating slices or arrays. Element counts inside `WireArray` and `WireMap` must never pre-allocate more capacity than the remaining bytes in the container can physically represent.
3. **Recursion Depth Limit:** Nested sub-messages, nested containers (`WireArray` and `WireMap`), unknown fields, and `Field #0` recursive schema references must enforce a maximum container recursion depth (`MaxDepth = 32` across Go and JS) and reject deeper nesting with a parse error.
4. **Strict UTF-8 on `WireString`:** Every `WireString` field (including map keys/values of type `WireString`, string array elements, and `Field #0` field names) must be valid UTF-8. Invalid UTF-8 byte sequences must be rejected during decoding.
5. **Known Unknown Extensions (`WireExtended`):** Unknown `ExtType` IDs inside `WireExtended` (`15`) must be skipped cleanly using `ByteLen`; truncated `WireExtended` payloads or unknown element wire types (`> 15`) inside `WireArray` / `WireMap` headers must be rejected.
