package omnibuf

import (
	"reflect"

	"github.com/omnibuf/omnibuf-go/wire"
)

// fixedWireType distinguishes signed integers, unsigned integers and floating
// point values everywhere: fields, array elements, and map entries.
func fixedWireType(t reflect.Type, wt uint8) uint8 {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if wt != wire.WireFixed32 && wt != wire.WireFixed64 {
		return wt
	}
	switch t.Kind() {
	case reflect.Float32:
		return wire.WireFloat32
	case reflect.Float64:
		return wire.WireFloat64
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if wt == wire.WireFixed32 {
			return wire.WireSfixed32
		}
		return wire.WireSfixed64
	}
	return wt
}
func finalizeWireType(fc *fieldCodec, t reflect.Type, opt string) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if fc.wireType == wire.WireArray {
		elem := t.Elem()
		for elem.Kind() == reflect.Pointer {
			elem = elem.Elem()
		}
		switch fc.kind {
		case kindStringSlice:
			fc.elemWireType = wire.WireString
			if opt == "bytes" {
				fc.elemWireType = wire.WireBytes
			}
		case kindBytesSlice:
			fc.elemWireType = wire.WireBytes
			if opt == "string" {
				fc.elemWireType = wire.WireString
			}
		case kindBoolSlice:
			fc.elemWireType = wire.WireBoolFalse
		case kindStructValSlice, kindStructPtrSlice:
			fc.elemWireType = wire.WireSubMsg
		case kindUint8Slice, kindUint16Slice, kindUint32Slice, kindUint64Slice:
			fc.elemWireType = wire.WireUint
		case kindInt8Slice, kindInt16Slice, kindInt32Slice, kindInt64Slice, kindUint8SintSlice, kindUint16SintSlice, kindUint32SintSlice, kindUint64SintSlice:
			fc.elemWireType = wire.WireSint
		default:
			wt := wire.WireFixed64
			switch fc.kind {
			case kindFloat32Slice, kindUint8Fixed32Slice, kindUint16Fixed32Slice, kindUint64Fixed32Slice, kindInt8Fixed32Slice, kindInt16Fixed32Slice, kindInt64Fixed32Slice:
				wt = wire.WireFixed32
			}
			fc.elemWireType = fixedWireType(elem, wt)
		}
	} else {
		fc.wireType = fixedWireType(t, fc.wireType)
	}
	if t.Kind() == reflect.Map {
		fc.mapKeyWT = fixedWireType(t.Key(), fc.mapKeyWT)
		fc.mapValWT = fixedWireType(t.Elem(), fc.mapValWT)
		if fc.mapKeyWT == wire.WireBoolTrue {
			fc.mapKeyWT = wire.WireBoolFalse
		}
		if fc.mapValWT == wire.WireBoolTrue {
			fc.mapValWT = wire.WireBoolFalse
		}
	}
}
