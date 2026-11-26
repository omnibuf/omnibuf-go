package omnibufproto

import (
	"reflect"
	"strconv"
	"strings"
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var reflectiveProtoTypes sync.Map

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
		if md.Syntax() == protoreflect.Editions {
			return true
		}
		if md.ExtensionRanges().Len() > 0 {
			return true
		}
		for i := 0; i < md.Fields().Len(); i++ {
			fd := md.Fields().Get(i)
			if fd.Number() > 65535 || fd.Cardinality() == protoreflect.Required || fd.ContainingOneof() != nil || fd.Kind() == protoreflect.EnumKind || (fd.HasPresence() && fd.Kind() != protoreflect.MessageKind) {
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

	matched := make(map[protoreflect.FieldNumber]bool, md.Fields().Len())
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.Anonymous && sf.Type.Implements(protoMessageType) {
			return false
		}
		if !sf.IsExported() {
			continue
		}
		pbTag := sf.Tag.Get("protobuf")
		if pbTag == "" || pbTag == "-" {
			continue
		}
		parts := strings.Split(pbTag, ",")
		if len(parts) < 2 {
			continue
		}
		fnum, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		fn := protoreflect.FieldNumber(fnum)
		fd := md.Fields().ByNumber(fn)
		if fd == nil {
			continue
		}
		matched[fn] = true
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
	return len(matched) == md.Fields().Len()
}
