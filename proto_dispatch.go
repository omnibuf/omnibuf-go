package omnibuf

import (
	"reflect"
	"strconv"
	"strings"
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var reflectiveProtoTypes sync.Map

// Open generated structs without descriptor-only features can retain the table
// fast path. Oneofs, enums, explicit scalar presence and large field IDs require
// descriptors. Walk child descriptors too, including recursive schemas.
func needsProtoReflection(pm proto.Message) bool {
	t := reflect.TypeOf(pm)
	if cached, ok := reflectiveProtoTypes.Load(t); ok {
		return cached.(bool)
	}
	seen := make(map[protoreflect.FullName]bool)
	var visit func(protoreflect.MessageDescriptor) bool
	visit = func(md protoreflect.MessageDescriptor) bool {
		if seen[md.FullName()] {
			return false
		}
		seen[md.FullName()] = true
		if md.ExtensionRanges().Len() > 0 {
			return true
		}
		for i := 0; i < md.Fields().Len(); i++ {
			fd := md.Fields().Get(i)
			if fd.Number() > 65535 || fd.ContainingOneof() != nil || fd.Kind() == protoreflect.EnumKind || (fd.HasPresence() && fd.Kind() != protoreflect.MessageKind) {
				return true
			}
			if fd.Message() != nil && visit(fd.Message()) {
				return true
			}
		}
		return false
	}
	md := pm.ProtoReflect().Descriptor()
	use := visit(md)
	// Opaque generated APIs expose no protobuf-tagged fields to the table codec,
	// including when an open root struct embeds an opaque child message struct.
	if !use {
		use = !hasAllOpenStructFields(t, md, make(map[reflect.Type]bool))
	}
	reflectiveProtoTypes.Store(t, use)
	return use
}

func hasAllOpenStructFields(t reflect.Type, md protoreflect.MessageDescriptor, visited map[reflect.Type]bool) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	if visited[t] {
		return true
	}
	visited[t] = true

	pbFieldCount := 0
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		pbTag := sf.Tag.Get("protobuf")
		if pbTag == "" || pbTag == "-" {
			continue
		}
		pbFieldCount++
		parts := strings.Split(pbTag, ",")
		if len(parts) < 2 {
			continue
		}
		fnum, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		fd := md.Fields().ByNumber(protoreflect.FieldNumber(fnum))
		if fd == nil {
			continue
		}
		switch {
		case fd.IsMap():
			if fd.MapValue().Message() != nil && sf.Type.Kind() == reflect.Map {
				if !hasAllOpenStructFields(sf.Type.Elem(), fd.MapValue().Message(), visited) {
					return false
				}
			}
		case fd.IsList():
			if fd.Message() != nil && sf.Type.Kind() == reflect.Slice {
				if !hasAllOpenStructFields(sf.Type.Elem(), fd.Message(), visited) {
					return false
				}
			}
		case fd.Message() != nil:
			if !hasAllOpenStructFields(sf.Type, fd.Message(), visited) {
				return false
			}
		}
	}
	return md.Fields().Len() == 0 || pbFieldCount > 0
}
