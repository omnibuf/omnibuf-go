package omnibufproto_test

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	_ "github.com/omnibuf/omnibuf-go/omnibufproto"

	. "github.com/omnibuf/omnibuf-go"
	. "github.com/omnibuf/omnibuf-go/wire"
	"google.golang.org/protobuf/encoding/protowire"
)

// Canonical benchmark schema types matching proto/omnibuf/bench/peer.proto and testdata/schemas/peer.{fbs,capnp}.

type Location struct {
	Region     string  `omnibuf:"1,name=region" protobuf:"bytes,1,opt,name=region,proto3" json:"region" msgpack:"region" avro:"region"`
	Datacenter string  `omnibuf:"2,name=datacenter" protobuf:"bytes,2,opt,name=datacenter,proto3" json:"datacenter" msgpack:"datacenter" avro:"datacenter"`
	City       string  `omnibuf:"3,name=city" protobuf:"bytes,3,opt,name=city,proto3" json:"city" msgpack:"city" avro:"city"`
	Rack       uint32  `omnibuf:"4,name=rack" protobuf:"varint,4,opt,name=rack,proto3" json:"rack" msgpack:"rack" avro:"rack"`
	Latitude   float32 `omnibuf:"5,name=latitude" protobuf:"fixed32,5,opt,name=latitude,proto3" json:"latitude" msgpack:"latitude" avro:"latitude"`
	Longitude  float32 `omnibuf:"6,name=longitude" protobuf:"fixed32,6,opt,name=longitude,proto3" json:"longitude" msgpack:"longitude" avro:"longitude"`
}

type Neighbor struct {
	PeerASN     uint32 `omnibuf:"1,name=peer_asn" protobuf:"varint,1,opt,name=peer_asn,proto3" json:"peer_asn" msgpack:"peer_asn" avro:"peer_asn"`
	Router      string `omnibuf:"2,name=router" protobuf:"bytes,2,opt,name=router,proto3" json:"router" msgpack:"router" avro:"router"`
	LatencyUS   uint32 `omnibuf:"3,name=latency_us" protobuf:"varint,3,opt,name=latency_us,proto3" json:"latency_us" msgpack:"latency_us" avro:"latency_us"`
	Established bool   `omnibuf:"4,name=established" protobuf:"varint,4,opt,name=established,proto3" json:"established" msgpack:"established" avro:"established"`
}

type PeerUpdate struct {
	ASN       uint32     `omnibuf:"1,name=asn" protobuf:"varint,1,opt,name=asn,proto3" json:"asn" msgpack:"asn" avro:"asn"`
	RouterID  string     `omnibuf:"2,name=router_id" protobuf:"bytes,2,opt,name=router_id,proto3" json:"router_id" msgpack:"router_id" avro:"router_id"`
	Active    bool       `omnibuf:"3,name=active" protobuf:"varint,3,opt,name=active,proto3" json:"active" msgpack:"active" avro:"active"`
	UptimeSec uint64     `omnibuf:"4,name=uptime_sec" protobuf:"varint,4,opt,name=uptime_sec,proto3" json:"uptime_sec" msgpack:"uptime_sec" avro:"uptime_sec"`
	RssiDbm   int32      `omnibuf:"5,name=rssi_dbm" protobuf:"zigzag32,5,opt,name=rssi_dbm,proto3" json:"rssi_dbm" msgpack:"rssi_dbm" avro:"rssi_dbm"`
	CPULoad   float64    `omnibuf:"6,name=cpu_load" protobuf:"fixed64,6,opt,name=cpu_load,proto3" json:"cpu_load" msgpack:"cpu_load" avro:"cpu_load"`
	Tags      []string   `omnibuf:"7,name=tags" protobuf:"bytes,7,rep,name=tags,proto3" json:"tags" msgpack:"tags" avro:"tags"`
	Prefixes  []uint32   `omnibuf:"8,name=prefixes" protobuf:"varint,8,rep,packed,name=prefixes,proto3" json:"prefixes" msgpack:"prefixes" avro:"prefixes"`
	Location  Location   `omnibuf:"9,name=location" protobuf:"bytes,9,opt,name=location,proto3" json:"location" msgpack:"location" avro:"location"`
	Neighbors []Neighbor `omnibuf:"10,name=neighbors" protobuf:"bytes,10,rep,name=neighbors,proto3" json:"neighbors" msgpack:"neighbors" avro:"neighbors"`
	Sequence  uint64     `omnibuf:"11,name=sequence" protobuf:"varint,11,opt,name=sequence,proto3" json:"sequence" msgpack:"sequence" avro:"sequence"`
}

// Field numbers in PeerUpdate, Location, and Neighbor.
const (
	FieldPeerASN       uint16 = 1
	FieldPeerRouterID  uint16 = 2
	FieldPeerActive    uint16 = 3
	FieldPeerUptimeSec uint16 = 4
	FieldPeerRssiDbm   uint16 = 5
	FieldPeerCPULoad   uint16 = 6
	FieldPeerTags      uint16 = 7
	FieldPeerPrefixes  uint16 = 8
	FieldPeerLocation  uint16 = 9
	FieldPeerNeighbors uint16 = 10
	FieldPeerSequence  uint16 = 11

	FieldLocRegion     uint16 = 1
	FieldLocDatacenter uint16 = 2
	FieldLocCity       uint16 = 3
	FieldLocRack       uint16 = 4
	FieldLocLatitude   uint16 = 5
	FieldLocLongitude  uint16 = 6

	FieldNeighPeerASN     uint16 = 1
	FieldNeighRouter      uint16 = 2
	FieldNeighLatencyUS   uint16 = 3
	FieldNeighEstablished uint16 = 4
)

var peerSchemaMeta = &SchemaMeta{
	TypeName: "PeerUpdate",
	Fields: map[uint32]string{
		1:  "asn",
		2:  "router_id",
		3:  "active",
		4:  "uptime_sec",
		5:  "rssi_dbm",
		6:  "cpu_load",
		7:  "tags",
		8:  "prefixes",
		9:  "location",
		10: "neighbors",
		11: "sequence",
	},
	Children: map[uint32]*SchemaMeta{
		9: {
			TypeName: "Location",
			Fields: map[uint32]string{
				1: "region",
				2: "datacenter",
				3: "city",
				4: "rack",
				5: "latitude",
				6: "longitude",
			},
		},
		10: {
			TypeName: "Neighbor",
			Fields: map[uint32]string{
				1: "peer_asn",
				2: "router",
				3: "latency_us",
				4: "established",
			},
		},
	},
}

// SamplePeerUpdate returns a representative BGP/Telemetry PeerUpdate payload.
func SamplePeerUpdate() PeerUpdate {
	return PeerUpdate{
		ASN:       64512,
		RouterID:  "edge-rtr-fra01.core.backbone.net",
		Active:    true,
		UptimeSec: 1849203,
		RssiDbm:   -62,
		CPULoad:   0.42875,
		Tags:      []string{"tier1", "ebgp", "eu-central", "ixp-de-cix"},
		Prefixes:  []uint32{167772160, 172032000, 3232235520, 2886729728, 134744072, 134743040},
		Location: Location{
			Region:     "eu-central-1",
			Datacenter: "FRA-Equinix-FR5",
			City:       "Frankfurt",
			Rack:       42,
			Latitude:   50.1109,
			Longitude:  8.6821,
		},
		Neighbors: []Neighbor{
			{PeerASN: 3356, Router: "ae0.fra1.level3.net", LatencyUS: 850, Established: true},
			{PeerASN: 1299, Router: "ffm-b5-link.telia.net", LatencyUS: 920, Established: true},
			{PeerASN: 174, Router: "be2040.ccr41.fra03.atlas.cogentco.com", LatencyUS: 1120, Established: true},
			{PeerASN: 2914, Router: "ae-12.r24.frnkge08.de.bb.gin.ntt.net", LatencyUS: 980, Established: false},
		},
		Sequence: 9876543210,
	}
}

// SampleLargeNestedPeerUpdate returns SamplePeerUpdate with >16 KiB strings inside
// Location.Datacenter (20,480 B) and Neighbors[0].Router (18,432 B), forcing three
// 2B-to-4B container length-header expansions during single-pass encoding.
func SampleLargeNestedPeerUpdate() PeerUpdate {
	p := SamplePeerUpdate()
	p.Location.Datacenter = strings.Repeat("d", 20480)
	p.Neighbors[0].Router = strings.Repeat("n", 18432)
	return p
}

// SampleManyNeighborsPeerUpdate returns SamplePeerUpdate with 32 Neighbor entries
// (repeating the 4 canonical neighbors 8x so ~1.3 KiB of trailing fields follow Location).
func SampleManyNeighborsPeerUpdate() PeerUpdate {
	p := SamplePeerUpdate()
	base := p.Neighbors
	neighs := make([]Neighbor, 0, 32)
	for range 8 {
		neighs = append(neighs, base...)
	}
	p.Neighbors = neighs
	return p
}

// ============================================================================
// 1. OmniBuf Encoder & Decoder
// ============================================================================

func growSlice[T any](dst []T, add int) ([]T, int) {
	start := len(dst)
	total := start + add
	if cap(dst) < total || dst == nil {
		next := make([]T, total)
		copy(next, dst)
		return next, start
	}
	return dst[:total], start
}

func sliceForDecode[T any](p *[]T, alreadySeen bool) []T {
	sl := *p
	if !alreadySeen && sl != nil {
		return sl[:0]
	}
	return sl
}

var peerSchemaMetaWire = peerSchemaMeta.AppendField0(nil, true)

// EncodePeerOmniBuf encodes PeerUpdate into enc in shared-schema mode (or self-describing mode if withField0 is true).
func EncodePeerOmniBuf(enc *Encoder, p *PeerUpdate, withField0 bool) []byte {
	enc.Reset()
	if withField0 {
		enc.WriteRaw(peerSchemaMetaWire)
	}
	enc.WriteUint(FieldPeerASN, uint64(p.ASN))
	enc.WriteString(FieldPeerRouterID, p.RouterID)
	enc.WriteBool(FieldPeerActive, p.Active)
	enc.WriteUint(FieldPeerUptimeSec, p.UptimeSec)
	enc.WriteInt(FieldPeerRssiDbm, int64(p.RssiDbm))
	enc.WriteFloat64(FieldPeerCPULoad, p.CPULoad)
	enc.WriteStringArr(FieldPeerTags, p.Tags)
	enc.WriteUint32Arr(FieldPeerPrefixes, p.Prefixes)

	// Nested sub-message: Location (single-pass 2B backpatched)
	enc.BeginSubMsg(FieldPeerLocation)
	enc.WriteString(FieldLocRegion, p.Location.Region)
	enc.WriteString(FieldLocDatacenter, p.Location.Datacenter)
	enc.WriteString(FieldLocCity, p.Location.City)
	enc.WriteUint(FieldLocRack, uint64(p.Location.Rack))
	enc.WriteFloat32(FieldLocLatitude, p.Location.Latitude)
	enc.WriteFloat32(FieldLocLongitude, p.Location.Longitude)
	enc.EndSubMsg()

	// Indexed sub-message array: Neighbors (WireSubMsgArr)
	enc.BeginSubMsgArr(FieldPeerNeighbors)
	for i := range p.Neighbors {
		nb := &p.Neighbors[i]
		enc.BeginSubMsgElem()
		enc.WriteUint(FieldNeighPeerASN, uint64(nb.PeerASN))
		enc.WriteString(FieldNeighRouter, nb.Router)
		enc.WriteUint(FieldNeighLatencyUS, uint64(nb.LatencyUS))
		enc.WriteBool(FieldNeighEstablished, nb.Established)
		enc.EndSubMsgElem()
	}
	enc.EndSubMsgArr()

	enc.WriteUint(FieldPeerSequence, p.Sequence)
	return enc.Finish()
}

// EncodePeerOmniBufDirect encodes PeerUpdate directly into dst []byte in a single forward pass
// (analogous to protowire.Append* operating on a slice in registers, without a two-pass Size() walk).
func EncodePeerOmniBufDirect(dst []byte, p *PeerUpdate) []byte {
	dst = dst[:0]

	// Field 1: ASN (tag = (1<<4)|0 = 16 -> 1B tag 0x21)
	dst = append(dst, byte((FieldPeerASN<<4|uint16(WireUint))<<1)|1)
	dst = AppendVarint(dst, uint64(p.ASN))

	// Field 2: RouterID (tag = (2<<4)|3 = 35 -> 1B tag)
	dst = append(dst, byte((FieldPeerRouterID<<4|uint16(WireString))<<1)|1)
	dst = AppendVarint(dst, uint64(len(p.RouterID)))
	dst = append(dst, p.RouterID...)

	// Field 3: Active (0-byte boolean in tag)
	if p.Active {
		dst = append(dst, byte((FieldPeerActive<<4|uint16(WireBoolTrue))<<1)|1)
	} else {
		dst = append(dst, byte((FieldPeerActive<<4|uint16(WireBoolFalse))<<1)|1)
	}

	// Field 4: UptimeSec
	dst = append(dst, byte((FieldPeerUptimeSec<<4|uint16(WireUint))<<1)|1)
	dst = AppendVarint(dst, p.UptimeSec)

	// Field 5: RssiDbm (ZigZag, WireSint)
	dst = append(dst, byte((FieldPeerRssiDbm<<4|uint16(WireSint))<<1)|1)
	dst = AppendVarint(dst, EncodeZigZag(int64(p.RssiDbm)))

	// Field 6: CPULoad (Float64)
	dst = append(dst, byte((FieldPeerCPULoad<<4|uint16(WireFloat64))<<1)|1)
	cpuBits := math.Float64bits(p.CPULoad)
	dst = append(dst,
		byte(cpuBits), byte(cpuBits>>8), byte(cpuBits>>16), byte(cpuBits>>24),
		byte(cpuBits>>32), byte(cpuBits>>40), byte(cpuBits>>48), byte(cpuBits>>56),
	)

	// Field 7: Tags (WireArray of WireString, 1B tag since fieldNum=7, backpatched length)
	dst = append(dst, byte((FieldPeerTags<<4|uint16(WireArray))<<1)|1, 0, 0, WireString)
	tagsLenPos := len(dst) - 3
	dst = AppendVarint(dst, uint64(len(p.Tags)))
	for i := range p.Tags {
		s := p.Tags[i]
		dst = AppendVarint(dst, uint64(len(s)))
		dst = append(dst, s...)
	}
	dst = BackpatchContainerLen(dst, tagsLenPos)

	// Field 8: Prefixes (WireArray of WireUint, 2B tag since fieldNum=8, backpatched length)
	tag8 := ((FieldPeerPrefixes<<4 | uint16(WireArray)) << 2) | 2
	dst = append(dst, byte(tag8), byte(tag8>>8), 0, 0, WireUint)
	prefLenPos := len(dst) - 3
	dst = AppendVarint(dst, uint64(len(p.Prefixes)))
	for i := range p.Prefixes {
		dst = AppendVarint(dst, uint64(p.Prefixes[i]))
	}
	dst = BackpatchContainerLen(dst, prefLenPos)

	// Field 9: Location (WireSubMsg, 1-pass backpatched)
	tag9 := ((FieldPeerLocation<<4 | uint16(WireSubMsg)) << 2) | 2
	dst = append(dst, byte(tag9), byte(tag9>>8), 0, 0)
	locLenPos := len(dst) - 2
	dst = append(dst, byte((FieldLocRegion<<4|uint16(WireString))<<1)|1)
	dst = AppendVarint(dst, uint64(len(p.Location.Region)))
	dst = append(dst, p.Location.Region...)
	dst = append(dst, byte((FieldLocDatacenter<<4|uint16(WireString))<<1)|1)
	dst = AppendVarint(dst, uint64(len(p.Location.Datacenter)))
	dst = append(dst, p.Location.Datacenter...)
	dst = append(dst, byte((FieldLocCity<<4|uint16(WireString))<<1)|1)
	dst = AppendVarint(dst, uint64(len(p.Location.City)))
	dst = append(dst, p.Location.City...)
	dst = append(dst, byte((FieldLocRack<<4|uint16(WireUint))<<1)|1)
	dst = AppendVarint(dst, uint64(p.Location.Rack))
	latBits := math.Float32bits(p.Location.Latitude)
	dst = append(dst, byte((FieldLocLatitude<<4|uint16(WireFloat32))<<1)|1,
		byte(latBits), byte(latBits>>8), byte(latBits>>16), byte(latBits>>24))
	lonBits := math.Float32bits(p.Location.Longitude)
	dst = append(dst, byte((FieldLocLongitude<<4|uint16(WireFloat32))<<1)|1,
		byte(lonBits), byte(lonBits>>8), byte(lonBits>>16), byte(lonBits>>24))
	dst = BackpatchContainerLen(dst, locLenPos)

	// Field 10: Neighbors (WireArray of WireSubMsg, 1-pass backpatched)
	tag10 := ((FieldPeerNeighbors<<4 | uint16(WireArray)) << 2) | 2
	dst = append(dst, byte(tag10), byte(tag10>>8), 0, 0, WireSubMsg)
	arrLenPos := len(dst) - 3
	dst = AppendVarint(dst, uint64(len(p.Neighbors)))
	for i := range p.Neighbors {
		nb := &p.Neighbors[i]
		eLenPos := len(dst)
		dst = append(dst, 0, 0) // Reserve 2B element length
		dst = append(dst, byte((FieldNeighPeerASN<<4|uint16(WireUint))<<1)|1)
		dst = AppendVarint(dst, uint64(nb.PeerASN))
		dst = append(dst, byte((FieldNeighRouter<<4|uint16(WireString))<<1)|1)
		dst = AppendVarint(dst, uint64(len(nb.Router)))
		dst = append(dst, nb.Router...)
		dst = append(dst, byte((FieldNeighLatencyUS<<4|uint16(WireUint))<<1)|1)
		dst = AppendVarint(dst, uint64(nb.LatencyUS))
		if nb.Established {
			dst = append(dst, byte((FieldNeighEstablished<<4|uint16(WireBoolTrue))<<1)|1)
		} else {
			dst = append(dst, byte((FieldNeighEstablished<<4|uint16(WireBoolFalse))<<1)|1)
		}
		dst = BackpatchContainerLen(dst, eLenPos)
	}
	dst = BackpatchContainerLen(dst, arrLenPos)

	// Field 11: Sequence
	tag11 := ((FieldPeerSequence<<4 | uint16(WireUint)) << 2) | 2
	dst = append(dst, byte(tag11), byte(tag11>>8))
	dst = AppendVarint(dst, p.Sequence)
	return dst
}

// DecodePeerOmniBufInto decodes an OmniBuf packet into out.
// If zeroCopyStrings is true, strings reference packet memory via unsafe.String (0 string allocs).
func DecodePeerOmniBufInto(packet []byte, out *PeerUpdate, zeroCopyStrings bool) error {
	n := len(packet)
	pos := 0
	var seenMask uint16
	for pos < n {
		b0 := packet[pos]
		var tag uint16
		if (b0 & 1) != 0 {
			tag = uint16(b0 >> 1)
			pos++
		} else {
			tag = binary.LittleEndian.Uint16(packet[pos:]) >> 2
			pos += 2
		}
		fieldNum := tag >> 4
		wt := uint8(tag & 0x0F)

		switch fieldNum {
		case 0:
			var byteLen uint64
			byteLen, pos = ReadVarintAt(packet, pos)
			pos += int(byteLen)
		case FieldPeerASN:
			var v uint64
			v, pos = ReadVarintAt(packet, pos)
			out.ASN = uint32(v)
		case FieldPeerRouterID:
			var sLen64 uint64
			sLen64, pos = ReadVarintAt(packet, pos)
			sLen := int(sLen64)
			out.RouterID = DecodeStr(packet[pos:pos+sLen], zeroCopyStrings)
			pos += sLen
		case FieldPeerActive:
			out.Active = (wt == WireBoolTrue)
		case FieldPeerUptimeSec:
			out.UptimeSec, pos = ReadVarintAt(packet, pos)
		case FieldPeerRssiDbm:
			var v uint64
			v, pos = ReadVarintAt(packet, pos)
			out.RssiDbm = int32(DecodeZigZag(v))
		case FieldPeerCPULoad:
			out.CPULoad = math.Float64frombits(binary.LittleEndian.Uint64(packet[pos:]))
			pos += 8
		case FieldPeerTags:
			var byteLen64 uint64
			byteLen64, pos = ReadVarintAt(packet, pos)
			end := pos + int(byteLen64)
			var c64 uint64
			c64, p := ReadVarintAt(packet, pos+1)
			alreadySeen := (seenMask & (1 << FieldPeerTags)) != 0
			seenMask |= (1 << FieldPeerTags)
			var start int
			out.Tags, start = growSlice(sliceForDecode(&out.Tags, alreadySeen), int(c64))
			tags := out.Tags[start:]
			for i := range tags {
				var sLen64 uint64
				sLen64, p = ReadVarintAt(packet, p)
				sLen := int(sLen64)
				tags[i] = DecodeStr(packet[p:p+sLen], zeroCopyStrings)
				p += sLen
			}
			pos = end
		case FieldPeerPrefixes:
			var byteLen64 uint64
			byteLen64, pos = ReadVarintAt(packet, pos)
			end := pos + int(byteLen64)
			var c64 uint64
			c64, p := ReadVarintAt(packet, pos+1)
			alreadySeen := (seenMask & (1 << FieldPeerPrefixes)) != 0
			seenMask |= (1 << FieldPeerPrefixes)
			var start int
			out.Prefixes, start = growSlice(sliceForDecode(&out.Prefixes, alreadySeen), int(c64))
			prefs := out.Prefixes[start:]
			for i := range prefs {
				var v uint64
				v, p = ReadVarintAt(packet, p)
				prefs[i] = uint32(v)
			}
			pos = end
		case FieldPeerLocation:
			var subLen64 uint64
			subLen64, pos = ReadVarintAt(packet, pos)
			subLen := int(subLen64)
			decodeLocationOmniBuf(packet[pos:pos+subLen], &out.Location, zeroCopyStrings)
			pos += subLen
		case FieldPeerNeighbors:
			var arrLen64 uint64
			arrLen64, pos = ReadVarintAt(packet, pos)
			arrLen := int(arrLen64)
			arrPayload := packet[pos : pos+arrLen]
			_, elemCount, p, _ := ParseArrayHeader(arrPayload)
			alreadySeen := (seenMask & (1 << FieldPeerNeighbors)) != 0
			seenMask |= (1 << FieldPeerNeighbors)
			var start int
			out.Neighbors, start = growSlice(sliceForDecode(&out.Neighbors, alreadySeen), elemCount)
			neighs := out.Neighbors[start:]
			for i := range elemCount {
				var elemLen64 uint64
				elemLen64, p = ReadVarintAt(arrPayload, p)
				elemLen := int(elemLen64)
				decodeNeighborOmniBuf(arrPayload[p:p+elemLen], &neighs[i], zeroCopyStrings)
				p += elemLen
			}
			pos += arrLen
		case FieldPeerSequence:
			out.Sequence, pos = ReadVarintAt(packet, pos)
		default:
			return fmt.Errorf("omnibuf: unknown field %d", fieldNum)
		}
	}
	return nil
}

func decodeLocationOmniBuf(packet []byte, loc *Location, zeroCopy bool) {
	n := len(packet)
	pos := 0
	for pos < n {
		fieldNum := uint16(packet[pos] >> 5)
		pos++
		switch fieldNum {
		case 0:
			var byteLen uint64
			byteLen, pos = ReadVarintAt(packet, pos)
			pos += int(byteLen)
		case FieldLocRegion:
			var sLen64 uint64
			sLen64, pos = ReadVarintAt(packet, pos)
			sLen := int(sLen64)
			loc.Region = DecodeStr(packet[pos:pos+sLen], zeroCopy)
			pos += sLen
		case FieldLocDatacenter:
			var sLen64 uint64
			sLen64, pos = ReadVarintAt(packet, pos)
			sLen := int(sLen64)
			loc.Datacenter = DecodeStr(packet[pos:pos+sLen], zeroCopy)
			pos += sLen
		case FieldLocCity:
			var sLen64 uint64
			sLen64, pos = ReadVarintAt(packet, pos)
			sLen := int(sLen64)
			loc.City = DecodeStr(packet[pos:pos+sLen], zeroCopy)
			pos += sLen
		case FieldLocRack:
			var v uint64
			v, pos = ReadVarintAt(packet, pos)
			loc.Rack = uint32(v)
		case FieldLocLatitude:
			loc.Latitude = math.Float32frombits(binary.LittleEndian.Uint32(packet[pos:]))
			pos += 4
		case FieldLocLongitude:
			loc.Longitude = math.Float32frombits(binary.LittleEndian.Uint32(packet[pos:]))
			pos += 4
		}
	}
}

func decodeNeighborOmniBuf(packet []byte, nb *Neighbor, zeroCopy bool) {
	n := len(packet)
	pos := 0
	for pos < n {
		tag := packet[pos] >> 1
		pos++
		fieldNum := uint16(tag >> 4)
		switch fieldNum {
		case 0:
			var byteLen uint64
			byteLen, pos = ReadVarintAt(packet, pos)
			pos += int(byteLen)
		case FieldNeighPeerASN:
			var v uint64
			v, pos = ReadVarintAt(packet, pos)
			nb.PeerASN = uint32(v)
		case FieldNeighRouter:
			var sLen64 uint64
			sLen64, pos = ReadVarintAt(packet, pos)
			sLen := int(sLen64)
			nb.Router = DecodeStr(packet[pos:pos+sLen], zeroCopy)
			pos += sLen
		case FieldNeighLatencyUS:
			var v uint64
			v, pos = ReadVarintAt(packet, pos)
			nb.LatencyUS = uint32(v)
		case FieldNeighEstablished:
			nb.Established = ((tag & 0x0F) == WireBoolTrue)
		}
	}
}

// ============================================================================
// 2. Classic Protobuf Wire Encoding (direct protowire)
// ============================================================================

// SizeLocationProto computes the exact LEB128 Protobuf wire size of Location (Pass 1 of 2-pass Protobuf).
func SizeLocationProto(loc *Location) int {
	sz := 0
	sz += 1 + protowire.SizeBytes(len(loc.Region))
	sz += 1 + protowire.SizeBytes(len(loc.Datacenter))
	sz += 1 + protowire.SizeBytes(len(loc.City))
	sz += 1 + protowire.SizeVarint(uint64(loc.Rack))
	sz += 1 + 4 // latitude fixed32
	sz += 1 + 4 // longitude fixed32
	return sz
}

// SizeNeighborProto computes the exact LEB128 Protobuf wire size of Neighbor (Pass 1 of 2-pass Protobuf).
func SizeNeighborProto(nb *Neighbor) int {
	sz := 0
	sz += 1 + protowire.SizeVarint(uint64(nb.PeerASN))
	sz += 1 + protowire.SizeBytes(len(nb.Router))
	sz += 1 + protowire.SizeVarint(uint64(nb.LatencyUS))
	sz += 1 + 1 // established bool
	return sz
}

// EncodePeerProtoWire encodes PeerUpdate using classic Protobuf TLV + LEB128 (2-pass Size + Write).
func EncodePeerProtoWire(dst []byte, p *PeerUpdate) []byte {
	dst = dst[:0]
	// Field 1: asn
	dst = protowire.AppendTag(dst, 1, protowire.VarintType)
	dst = protowire.AppendVarint(dst, uint64(p.ASN))
	// Field 2: router_id
	dst = protowire.AppendTag(dst, 2, protowire.BytesType)
	dst = protowire.AppendString(dst, p.RouterID)
	// Field 3: active
	dst = protowire.AppendTag(dst, 3, protowire.VarintType)
	if p.Active {
		dst = protowire.AppendVarint(dst, 1)
	} else {
		dst = protowire.AppendVarint(dst, 0)
	}
	// Field 4: uptime_sec
	dst = protowire.AppendTag(dst, 4, protowire.VarintType)
	dst = protowire.AppendVarint(dst, p.UptimeSec)
	// Field 5: rssi_dbm (sint32 ZigZag)
	dst = protowire.AppendTag(dst, 5, protowire.VarintType)
	dst = protowire.AppendVarint(dst, protowire.EncodeZigZag(int64(p.RssiDbm)))
	// Field 6: cpu_load (double fixed64)
	dst = protowire.AppendTag(dst, 6, protowire.Fixed64Type)
	dst = protowire.AppendFixed64(dst, math.Float64bits(p.CPULoad))
	// Field 7: repeated string tags
	for i := range p.Tags {
		dst = protowire.AppendTag(dst, 7, protowire.BytesType)
		dst = protowire.AppendString(dst, p.Tags[i])
	}
	// Field 8: packed repeated uint32 prefixes (requires computing packed byte length first!)
	packedLen := 0
	for i := range p.Prefixes {
		packedLen += protowire.SizeVarint(uint64(p.Prefixes[i]))
	}
	dst = protowire.AppendTag(dst, 8, protowire.BytesType)
	dst = protowire.AppendVarint(dst, uint64(packedLen))
	for i := range p.Prefixes {
		dst = protowire.AppendVarint(dst, uint64(p.Prefixes[i]))
	}
	// Field 9: Location sub-message (requires SizeLocationProto pre-pass!)
	locSize := SizeLocationProto(&p.Location)
	dst = protowire.AppendTag(dst, 9, protowire.BytesType)
	dst = protowire.AppendVarint(dst, uint64(locSize))
	dst = protowire.AppendTag(dst, 1, protowire.BytesType)
	dst = protowire.AppendString(dst, p.Location.Region)
	dst = protowire.AppendTag(dst, 2, protowire.BytesType)
	dst = protowire.AppendString(dst, p.Location.Datacenter)
	dst = protowire.AppendTag(dst, 3, protowire.BytesType)
	dst = protowire.AppendString(dst, p.Location.City)
	dst = protowire.AppendTag(dst, 4, protowire.VarintType)
	dst = protowire.AppendVarint(dst, uint64(p.Location.Rack))
	dst = protowire.AppendTag(dst, 5, protowire.Fixed32Type)
	dst = protowire.AppendFixed32(dst, math.Float32bits(p.Location.Latitude))
	dst = protowire.AppendTag(dst, 6, protowire.Fixed32Type)
	dst = protowire.AppendFixed32(dst, math.Float32bits(p.Location.Longitude))
	// Field 10: repeated Neighbor sub-messages (each requires SizeNeighborProto pre-pass!)
	for i := range p.Neighbors {
		nb := &p.Neighbors[i]
		nbSize := SizeNeighborProto(nb)
		dst = protowire.AppendTag(dst, 10, protowire.BytesType)
		dst = protowire.AppendVarint(dst, uint64(nbSize))
		dst = protowire.AppendTag(dst, 1, protowire.VarintType)
		dst = protowire.AppendVarint(dst, uint64(nb.PeerASN))
		dst = protowire.AppendTag(dst, 2, protowire.BytesType)
		dst = protowire.AppendString(dst, nb.Router)
		dst = protowire.AppendTag(dst, 3, protowire.VarintType)
		dst = protowire.AppendVarint(dst, uint64(nb.LatencyUS))
		dst = protowire.AppendTag(dst, 4, protowire.VarintType)
		if nb.Established {
			dst = protowire.AppendVarint(dst, 1)
		} else {
			dst = protowire.AppendVarint(dst, 0)
		}
	}
	// Field 11: sequence
	dst = protowire.AppendTag(dst, 11, protowire.VarintType)
	dst = protowire.AppendVarint(dst, p.Sequence)
	return dst
}

// DecodePeerProtoWireInto decodes a classic Protobuf packet into out using direct protowire.
func DecodePeerProtoWireInto(b []byte, out *PeerUpdate, zeroCopy bool) error {
	out.Tags = out.Tags[:0]
	out.Prefixes = out.Prefixes[:0]
	out.Neighbors = out.Neighbors[:0]
	for len(b) > 0 {
		num, wtype, n := protowire.ConsumeTag(b)
		if n < 0 {
			return protowire.ParseError(n)
		}
		b = b[n:]
		switch num {
		case 1:
			v, vn := protowire.ConsumeVarint(b)
			b = b[vn:]
			out.ASN = uint32(v)
		case 2:
			s, sn := protowire.ConsumeBytes(b)
			b = b[sn:]
			out.RouterID = DecodeStr(s, zeroCopy)
		case 3:
			v, vn := protowire.ConsumeVarint(b)
			b = b[vn:]
			out.Active = v != 0
		case 4:
			v, vn := protowire.ConsumeVarint(b)
			b = b[vn:]
			out.UptimeSec = v
		case 5:
			v, vn := protowire.ConsumeVarint(b)
			b = b[vn:]
			out.RssiDbm = int32(protowire.DecodeZigZag(v))
		case 6:
			v, vn := protowire.ConsumeFixed64(b)
			b = b[vn:]
			out.CPULoad = math.Float64frombits(v)
		case 7:
			s, sn := protowire.ConsumeBytes(b)
			b = b[sn:]
			out.Tags = append(out.Tags, DecodeStr(s, zeroCopy))
		case 8:
			packed, pn := protowire.ConsumeBytes(b)
			b = b[pn:]
			for len(packed) > 0 {
				v, vn := protowire.ConsumeVarint(packed)
				packed = packed[vn:]
				out.Prefixes = append(out.Prefixes, uint32(v))
			}
		case 9:
			sub, sn := protowire.ConsumeBytes(b)
			b = b[sn:]
			for len(sub) > 0 {
				fnum, _, fn := protowire.ConsumeTag(sub)
				sub = sub[fn:]
				switch fnum {
				case 1:
					s, vn := protowire.ConsumeBytes(sub)
					sub = sub[vn:]
					out.Location.Region = DecodeStr(s, zeroCopy)
				case 2:
					s, vn := protowire.ConsumeBytes(sub)
					sub = sub[vn:]
					out.Location.Datacenter = DecodeStr(s, zeroCopy)
				case 3:
					s, vn := protowire.ConsumeBytes(sub)
					sub = sub[vn:]
					out.Location.City = DecodeStr(s, zeroCopy)
				case 4:
					v, vn := protowire.ConsumeVarint(sub)
					sub = sub[vn:]
					out.Location.Rack = uint32(v)
				case 5:
					v, vn := protowire.ConsumeFixed32(sub)
					sub = sub[vn:]
					out.Location.Latitude = math.Float32frombits(v)
				case 6:
					v, vn := protowire.ConsumeFixed32(sub)
					sub = sub[vn:]
					out.Location.Longitude = math.Float32frombits(v)
				}
			}
		case 10:
			sub, sn := protowire.ConsumeBytes(b)
			b = b[sn:]
			var nb Neighbor
			for len(sub) > 0 {
				fnum, _, fn := protowire.ConsumeTag(sub)
				sub = sub[fn:]
				switch fnum {
				case 1:
					v, vn := protowire.ConsumeVarint(sub)
					sub = sub[vn:]
					nb.PeerASN = uint32(v)
				case 2:
					s, vn := protowire.ConsumeBytes(sub)
					sub = sub[vn:]
					nb.Router = DecodeStr(s, zeroCopy)
				case 3:
					v, vn := protowire.ConsumeVarint(sub)
					sub = sub[vn:]
					nb.LatencyUS = uint32(v)
				case 4:
					v, vn := protowire.ConsumeVarint(sub)
					sub = sub[vn:]
					nb.Established = v != 0
				}
			}
			out.Neighbors = append(out.Neighbors, nb)
		case 11:
			v, vn := protowire.ConsumeVarint(b)
			b = b[vn:]
			out.Sequence = v
		default:
			vn := protowire.ConsumeFieldValue(num, wtype, b)
			b = b[vn:]
		}
	}
	return nil
}

// LookupProtoWireNestedCity scans a Protobuf packet sequentially to extract peer.location.city
// without allocating structs (the fastest possible way to read a nested field in Protobuf).
func LookupProtoWireNestedCity(b []byte, zeroCopy bool) (string, bool) {
	for len(b) > 0 {
		num, wtype, n := protowire.ConsumeTag(b)
		if n < 0 {
			return "", false
		}
		b = b[n:]
		if num == 9 { // location
			sub, sn := protowire.ConsumeBytes(b)
			if sn < 0 {
				return "", false
			}
			for len(sub) > 0 {
				fnum, fwtype, fn := protowire.ConsumeTag(sub)
				if fn < 0 {
					return "", false
				}
				sub = sub[fn:]
				if fnum == 3 { // city
					s, vn := protowire.ConsumeBytes(sub)
					if vn < 0 {
						return "", false
					}
					return DecodeStr(s, zeroCopy), true
				}
				vn := protowire.ConsumeFieldValue(fnum, fwtype, sub)
				if vn < 0 {
					return "", false
				}
				sub = sub[vn:]
			}
			return "", false
		}
		vn := protowire.ConsumeFieldValue(num, wtype, b)
		if vn < 0 {
			return "", false
		}
		b = b[vn:]
	}
	return "", false
}

// LookupProtoWireNeighborRouter scans a Protobuf packet sequentially to extract peer.neighbors[targetIdx].router.
func LookupProtoWireNeighborRouter(b []byte, targetIdx int, zeroCopy bool) (string, bool) {
	seen := 0
	for len(b) > 0 {
		num, wtype, n := protowire.ConsumeTag(b)
		if n < 0 {
			return "", false
		}
		b = b[n:]
		if num == 10 { // neighbors
			sub, sn := protowire.ConsumeBytes(b)
			if sn < 0 {
				return "", false
			}
			if seen == targetIdx {
				for len(sub) > 0 {
					fnum, fwtype, fn := protowire.ConsumeTag(sub)
					if fn < 0 {
						return "", false
					}
					sub = sub[fn:]
					if fnum == 2 { // router
						s, vn := protowire.ConsumeBytes(sub)
						if vn < 0 {
							return "", false
						}
						return DecodeStr(s, zeroCopy), true
					}
					vn := protowire.ConsumeFieldValue(fnum, fwtype, sub)
					if vn < 0 {
						return "", false
					}
					sub = sub[vn:]
				}
				return "", false
			}
			seen++
			b = b[sn:]
			continue
		}
		vn := protowire.ConsumeFieldValue(num, wtype, b)
		if vn < 0 {
			return "", false
		}
		b = b[vn:]
	}
	return "", false
}

// MarshalJSON encodes PeerUpdate using standard encoding/json.
func MarshalJSON(p *PeerUpdate) ([]byte, error) {
	return json.Marshal(p)
}

// UnmarshalJSON decodes a JSON packet into out.
func UnmarshalJSON(b []byte, out *PeerUpdate) error {
	return json.Unmarshal(b, out)
}
