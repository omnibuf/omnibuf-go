export interface FormatStat {
  label: string;
  score: number;
}

export interface FormatProfile {
  key: string;
  name: string;
  url?: string;
  verdict?: string;
  stats: FormatStat[];
}

export interface BenchmarkRow {
  name: string;
  val: number;
  highlight?: boolean;
  baseline?: boolean;
}

export interface BenchmarkCategory {
  title: string;
  unit: string;
  maxVal: number;
  methodology: string;
  rows: BenchmarkRow[];
}

export interface EsBenchmarkRow {
  name: string;
  val: number;
  ops: string;
  ratio: string;
  sizeBytes?: number;
  highlight?: boolean;
  baseline?: boolean;
}

export interface EsBenchmarkCategory {
  title: string;
  unit: string;
  maxVal: number;
  methodology: string;
  rows: EsBenchmarkRow[];
}

export const formatProfiles: Record<string, FormatProfile> = {
  omnibuf: {
    key: 'omnibuf',
    name: 'OmniBuf',
    verdict:
      "Each chart scores five things from our benchmark suite. Encode and Decode Speed measure turning native structs into bytes and back. Memory Efficiency reflects heap allocations (buffer reuse and zeroCopy strings keep Go allocations at zero). Wire Size is the byte length on the 11-field PeerUpdate packet. Field Lookup measures reading one nested field out of raw bytes: jumping by byte length beats scanning varints, though FlatBuffers and Cap'n Proto are still faster here since they jump straight to an offset.",
    stats: [
      { label: 'Wire Size', score: 94 },
      { label: 'Encode Speed', score: 100 },
      { label: 'Decode Speed', score: 100 },
      { label: 'Field Lookup', score: 78 },
      { label: 'Memory Efficiency', score: 98 },
    ],
  },
  protobuf: {
    key: 'protobuf',
    name: 'Protobuf',
    url: 'https://protobuf.dev/',
    verdict:
      'Protobuf is ~7% smaller on the wire (341 B vs 366 B) and has libraries for almost every language on earth. OmniBuf lets you keep the same .proto schemas and generated structs, encodes in a single pass (~6x faster in Go), decodes 3x to 7x faster, and does not need the .proto file to inspect a packet.',
    stats: [
      { label: 'Wire Size', score: 100 },
      { label: 'Encode Speed', score: 45 },
      { label: 'Decode Speed', score: 42 },
      { label: 'Field Lookup', score: 72 },
      { label: 'Memory Efficiency', score: 65 },
    ],
  },
  flatbuffers: {
    key: 'flatbuffers',
    name: 'FlatBuffers',
    url: 'https://flatbuffers.dev/',
    verdict:
      'FlatBuffers reads fields straight out of a buffer by vtable offset, making it tough to beat for plucking two fields out of a huge message. The catch is authoring .fbs schemas and constructing messages through a builder API. On normal Go structs or TypeScript objects, OmniBuf is 39% smaller on the wire and much faster to encode.',
    stats: [
      { label: 'Wire Size', score: 57 },
      { label: 'Encode Speed', score: 60 },
      { label: 'Decode Speed', score: 90 },
      { label: 'Field Lookup', score: 98 },
      { label: 'Memory Efficiency', score: 98 },
    ],
  },
  capnproto: {
    key: 'capnproto',
    name: "Cap'n Proto",
    url: 'https://capnproto.org/',
    verdict:
      "Cap'n Proto lays out messages in memory as fixed-offset pointer structures and has its own RPC protocol with promise pipelining. Reading a single field in place takes 6 ns. Serializing normal language structs still requires copying fields in and out of segment builders: OmniBuf is 35% smaller and several times faster to encode and decode.",
    stats: [
      { label: 'Wire Size', score: 61 },
      { label: 'Encode Speed', score: 56 },
      { label: 'Decode Speed', score: 68 },
      { label: 'Field Lookup', score: 95 },
      { label: 'Memory Efficiency', score: 72 },
    ],
  },
  msgpack: {
    key: 'msgpack',
    name: 'MessagePack',
    url: 'https://msgpack.org/',
    verdict:
      'MessagePack fits arbitrary, untyped maps that have no schema at all. As a binary JSON encoding, an array of structs still repeats the string field names inside every element. OmniBuf is 44% smaller (and still 4% smaller even with Field #0 field names turned on, since array field names are only written once) and much faster in both Go and JS.',
    stats: [
      { label: 'Wire Size', score: 52 },
      { label: 'Encode Speed', score: 15 },
      { label: 'Decode Speed', score: 18 },
      { label: 'Field Lookup', score: 42 },
      { label: 'Memory Efficiency', score: 25 },
    ],
  },
  json: {
    key: 'json',
    name: 'JSON',
    url: 'https://www.json.org/',
    verdict:
      'JSON is the only format here that works straight in curl and jq with no schema or tooling. Once JSON parsing or payload size becomes a bottleneck, OmniBuf keeps the same Go struct-tag workflow (and defineMessage in TypeScript) at less than half the wire size and a fraction of the CPU time.',
    stats: [
      { label: 'Wire Size', score: 42 },
      { label: 'Encode Speed', score: 12 },
      { label: 'Decode Speed', score: 10 },
      { label: 'Field Lookup', score: 10 },
      { label: 'Memory Efficiency', score: 38 },
    ],
  },
  avro: {
    key: 'avro',
    name: 'Apache Avro',
    url: 'https://avro.apache.org/',
    verdict:
      "Avro is everywhere in Kafka and Spark. Raw Avro strips out all field tags and byte lengths (making it 313 B vs OmniBuf's 366 B), which means you cannot decode a packet or even skip a single field unless the exact writer schema is loaded in memory. Avro container files (OCF) go the other way and embed the full JSON schema in the header (1,359 B).",
    stats: [
      { label: 'Wire Size', score: 98 },
      { label: 'Encode Speed', score: 58 },
      { label: 'Decode Speed', score: 66 },
      { label: 'Field Lookup', score: 48 },
      { label: 'Memory Efficiency', score: 78 },
    ],
  },
  parquet: {
    key: 'parquet',
    name: 'Apache Parquet',
    url: 'https://parquet.apache.org/',
    verdict:
      'Parquet is not in the single-message charts; it is a columnar file format meant for batch analytics over thousands of rows, not RPC packets. Even a 1-row Parquet file carries column chunks, page headers, and a multi-kilobyte Thrift footer. Parquet fits event batches in S3 for DuckDB or Spark; OmniBuf fits individual messages over the network.',
    stats: [
      { label: 'Wire Size', score: 15 },
      { label: 'Encode Speed', score: 8 },
      { label: 'Decode Speed', score: 10 },
      { label: 'Field Lookup', score: 20 },
      { label: 'Memory Efficiency', score: 20 },
    ],
  },
};

export const opponentKeys = [
  'protobuf',
  'flatbuffers',
  'capnproto',
  'avro',
  'parquet',
  'msgpack',
  'json',
] as const;

// Raw output and commands: testdata/benchmarks.txt.
export const benchmarkCategories: BenchmarkCategory[] = [
  {
    title: 'Full Message Encode Speed',
    unit: 'ns',
    maxVal: 2400,
    methodology:
      "Serializing the 11-field Go struct into a byte slice. FlatBuffers and Cap'n Proto times include building the buffer from the struct.",
    rows: [
      { name: 'OmniBuf (Generated)', val: 126, highlight: true, baseline: true },
      { name: 'OmniBuf (Go Struct Tags)', val: 354, highlight: true },
      { name: 'Avro (Raw Binary)', val: 543 },
      { name: 'FlatBuffers', val: 725 },
      { name: 'Protobuf', val: 798 },
      { name: "Cap'n Proto", val: 1390 },
      { name: 'JSON (encoding/json)', val: 2229 },
      { name: 'MessagePack', val: 2241 },
      { name: 'JSON (encoding/json/v2)', val: 2261 },
    ],
  },
  {
    title: 'Full Message Decode Speed',
    unit: 'ns',
    maxVal: 4600,
    methodology:
      'Unpacking all 11 fields into a struct. Most libraries reuse the destination struct; standard Go Protobuf allocates a fresh one. Zero-copy strings are enabled where supported.',
    rows: [
      { name: 'OmniBuf (Generated)', val: 251, highlight: true, baseline: true },
      { name: 'FlatBuffers', val: 253 },
      { name: 'OmniBuf (Go Struct Tags)', val: 453, highlight: true },
      { name: 'Avro (Raw Binary)', val: 540 },
      { name: "Cap'n Proto", val: 993 },
      { name: 'Protobuf', val: 1368 },
      { name: 'MessagePack', val: 2838 },
      { name: 'JSON (encoding/json/v2)', val: 3589 },
      { name: 'JSON (encoding/json)', val: 4543 },
    ],
  },
  {
    title: 'Read a Single Nested Field (peer.location.city)',
    unit: 'ns',
    maxVal: 1400,
    methodology:
      "Reading peer.location.city directly from the raw byte slice. Cap'n Proto and FlatBuffers use offset tables; OmniBuf hops over preceding field headers by length.",
    rows: [
      { name: "Cap'n Proto", val: 6 },
      { name: 'FlatBuffers', val: 35 },
      { name: 'OmniBuf', val: 123, highlight: true, baseline: true },
      { name: 'Avro (Raw Binary)', val: 644 },
      { name: 'MessagePack', val: 777 },
      { name: 'Protobuf', val: 1314 },
    ],
  },
  {
    title: 'Selective Single-Field Mutation (peer.sequence)',
    unit: 'ns',
    maxVal: 7200,
    methodology:
      'Checking peer.location.city, incrementing the 64-bit sequence number, and returning the updated packet. OmniBuf splices the field in place; the others decode and re-encode.',
    rows: [
      { name: 'OmniBuf (Opaque Splice)', val: 287, highlight: true, baseline: true },
      { name: 'FlatBuffers', val: 984 },
      { name: 'Avro (Raw Binary)', val: 1111 },
      { name: 'Protobuf', val: 2056 },
      { name: "Cap'n Proto", val: 2482 },
      { name: 'MessagePack', val: 5106 },
      { name: 'JSON (encoding/json)', val: 7038 },
    ],
  },
  {
    title: 'Packet Size on the Wire',
    unit: 'B',
    maxVal: 1400,
    methodology:
      'Encoded byte size for the 11-field PeerUpdate message. Raw Avro omits all tags and lengths (requiring the writer schema to read), and Avro OCF embeds the JSON schema header.',
    rows: [
      { name: 'Avro (Raw, No Field Tags)', val: 313 },
      { name: 'Protobuf', val: 341 },
      { name: 'OmniBuf', val: 366, highlight: true, baseline: true },
      { name: "Cap'n Proto", val: 560 },
      { name: 'FlatBuffers', val: 600 },
      { name: 'OmniBuf (With Field #0 Names)', val: 630, highlight: true },
      { name: 'MessagePack', val: 656 },
      { name: 'JSON', val: 803 },
      { name: 'Avro OCF (With Schema Header)', val: 1359 },
    ],
  },
];

// TypeScript results imported from omnibuf-es/testdata/benchmarks.json (Node.js v24.22.1).
export const esBenchmarkCategories: EsBenchmarkCategory[] = [
  {
    title: 'Full Message Encode Speed',
    unit: 'ns',
    maxVal: 6200,
    methodology: 'PeerUpdate object -> Uint8Array (366B).',
    rows: [
      {
        name: 'OmniBuf',
        val: 1186,
        ops: '843,185 ops/s',
        ratio: '1.00x (base)',
        sizeBytes: 366,
        highlight: true,
        baseline: true,
      },
      {
        name: 'protobufjs',
        val: 1722,
        ops: '580,603 ops/s',
        ratio: '1.45x slower',
        sizeBytes: 350,
      },
      {
        name: 'JSON',
        val: 2338,
        ops: '427,662 ops/s',
        ratio: '1.97x slower',
        sizeBytes: 791,
      },
      {
        name: '@bufbuild/protobuf (protobuf-es)',
        val: 2771,
        ops: '360,850 ops/s',
        ratio: '2.34x slower',
        sizeBytes: 350,
      },
      {
        name: '@msgpack/msgpack',
        val: 4106,
        ops: '243,523 ops/s',
        ratio: '3.46x slower',
        sizeBytes: 622,
      },
      {
        name: 'google-protobuf',
        val: 6014,
        ops: '166,268 ops/s',
        ratio: '5.07x slower',
        sizeBytes: 350,
      },
    ],
  },
  {
    title: 'Full Message Decode Speed',
    unit: 'ns',
    maxVal: 4000,
    methodology: 'Uint8Array (366B) -> PeerUpdate object.',
    rows: [
      {
        name: 'OmniBuf',
        val: 1093,
        ops: '914,856 ops/s',
        ratio: '1.00x (base)',
        highlight: true,
        baseline: true,
      },
      {
        name: 'protobufjs',
        val: 1544,
        ops: '647,704 ops/s',
        ratio: '1.41x slower',
      },
      {
        name: 'JSON',
        val: 2400,
        ops: '416,720 ops/s',
        ratio: '2.20x slower',
      },
      {
        name: 'google-protobuf',
        val: 3462,
        ops: '288,871 ops/s',
        ratio: '3.17x slower',
      },
      {
        name: '@bufbuild/protobuf (protobuf-es)',
        val: 3784,
        ops: '264,277 ops/s',
        ratio: '3.46x slower',
      },
      {
        name: '@msgpack/msgpack',
        val: 3862,
        ops: '258,960 ops/s',
        ratio: '3.53x slower',
      },
    ],
  },
  {
    title: 'Read a Single Nested Field (peer.location.city)',
    unit: 'ns',
    maxVal: 4000,
    methodology:
      'Reading peer.location.city from a Uint8Array. The other JS libraries have to decode the whole message first; OmniBuf skips earlier fields by byte length.',
    rows: [
      {
        name: 'OmniBuf',
        val: 397,
        ops: '2,521,304 ops/s',
        ratio: '1.00x (base)',
        highlight: true,
        baseline: true,
      },
      {
        name: 'protobufjs',
        val: 1557,
        ops: '642,194 ops/s',
        ratio: '3.93x slower',
      },
      {
        name: 'JSON',
        val: 2418,
        ops: '413,481 ops/s',
        ratio: '6.10x slower',
      },
      {
        name: 'google-protobuf',
        val: 3393,
        ops: '294,692 ops/s',
        ratio: '8.56x slower',
      },
      {
        name: '@msgpack/msgpack',
        val: 3634,
        ops: '275,180 ops/s',
        ratio: '9.16x slower',
      },
      {
        name: '@bufbuild/protobuf (protobuf-es)',
        val: 3790,
        ops: '263,842 ops/s',
        ratio: '9.56x slower',
      },
    ],
  },
  {
    title: 'Selective Single-Field Mutation (peer.sequence)',
    unit: 'ns',
    maxVal: 9600,
    methodology: 'peer.sequence++ in the packet buffer -> updated Uint8Array.',
    rows: [
      {
        name: 'OmniBuf',
        val: 554,
        ops: '1,804,620 ops/s',
        ratio: '1.00x (base)',
        highlight: true,
        baseline: true,
      },
      {
        name: 'protobufjs',
        val: 3739,
        ops: '267,439 ops/s',
        ratio: '6.75x slower',
      },
      {
        name: 'JSON',
        val: 4731,
        ops: '211,375 ops/s',
        ratio: '8.54x slower',
      },
      {
        name: '@bufbuild/protobuf (protobuf-es)',
        val: 6987,
        ops: '143,117 ops/s',
        ratio: '12.61x slower',
      },
      {
        name: '@msgpack/msgpack',
        val: 8070,
        ops: '123,922 ops/s',
        ratio: '14.56x slower',
      },
      {
        name: 'google-protobuf',
        val: 9403,
        ops: '106,345 ops/s',
        ratio: '16.97x slower',
      },
    ],
  },
];
