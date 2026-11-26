package main

import (
	"fmt"
	"math"
	"strings"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

func setSupportedFeatures(gen *protogen.Plugin) {
	gen.SupportedFeatures = uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL | pluginpb.CodeGeneratorResponse_FEATURE_SUPPORTS_EDITIONS)
	gen.SupportedEditionsMinimum = descriptorpb.Edition_EDITION_2023
	gen.SupportedEditionsMaximum = descriptorpb.Edition_EDITION_2026
}

func isSupportedEdition(ed descriptorpb.Edition) bool {
	switch ed {
	case descriptorpb.Edition_EDITION_2023,
		descriptorpb.Edition_EDITION_2024,
		descriptorpb.Edition_EDITION_2026:
		return true
	default:
		return false
	}
}

func fileEdition(fd protoreflect.FileDescriptor) descriptorpb.Edition {
	if fd == nil {
		return descriptorpb.Edition_EDITION_UNKNOWN
	}
	desc := fd
	if imp, ok := fd.(protoreflect.FileImport); ok {
		desc = imp.FileDescriptor
	}
	if ed, ok := desc.(interface{ Edition() int32 }); ok {
		return descriptorpb.Edition(ed.Edition())
	}
	return descriptorpb.Edition_EDITION_UNKNOWN
}

func isCustomDescriptorOptionExtension(ext protoreflect.ExtensionDescriptor) bool {
	target := ext.ContainingMessage()
	if target == nil {
		return false
	}
	const prefix = "google.protobuf."
	const suffix = "Options"
	name := string(target.FullName())
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) || len(name) <= len(prefix)+len(suffix) {
		return false
	}
	return !strings.Contains(name[len(prefix):], ".")
}

func isZeroDefaultValue(fd protoreflect.FieldDescriptor) bool {
	v := fd.Default()
	switch fd.Kind() {
	case protoreflect.BoolKind:
		return !v.Bool()
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return v.Int() == 0
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return v.Uint() == 0
	case protoreflect.FloatKind:
		return math.Float32bits(float32(v.Float())) == 0
	case protoreflect.DoubleKind:
		return math.Float64bits(v.Float()) == 0
	case protoreflect.StringKind:
		return v.String() == ""
	case protoreflect.BytesKind:
		return len(v.Bytes()) == 0
	case protoreflect.EnumKind:
		return v.Enum() == 0
	default:
		return false
	}
}

func fieldEnforcesUTF8(fd protoreflect.FieldDescriptor) bool {
	if u, ok := fd.(interface{ EnforceUTF8() bool }); ok {
		return u.EnforceUTF8()
	}
	if opts, ok := fd.Options().(*descriptorpb.FieldOptions); ok {
		if v := opts.GetFeatures().GetUtf8Validation(); v != descriptorpb.FeatureSet_UTF8_VALIDATION_UNKNOWN {
			return v == descriptorpb.FeatureSet_VERIFY
		}
	}
	for p := fd.Parent(); p != nil; p = p.Parent() {
		switch d := p.(type) {
		case protoreflect.MessageDescriptor:
			if opts, ok := d.Options().(*descriptorpb.MessageOptions); ok {
				if v := opts.GetFeatures().GetUtf8Validation(); v != descriptorpb.FeatureSet_UTF8_VALIDATION_UNKNOWN {
					return v == descriptorpb.FeatureSet_VERIFY
				}
			}
		case protoreflect.FileDescriptor:
			if opts, ok := d.Options().(*descriptorpb.FileOptions); ok {
				if v := opts.GetFeatures().GetUtf8Validation(); v != descriptorpb.FeatureSet_UTF8_VALIDATION_UNKNOWN {
					return v == descriptorpb.FeatureSet_VERIFY
				}
			}
		}
	}
	return true
}

func mapFieldDisablesUTF8(mapFD, entryFD protoreflect.FieldDescriptor) bool {
	if !fieldEnforcesUTF8(entryFD) {
		return true
	}
	mapOpts, ok := mapFD.Options().(*descriptorpb.FieldOptions)
	if !ok || mapOpts.GetFeatures().GetUtf8Validation() != descriptorpb.FeatureSet_NONE {
		return false
	}
	entryOpts, ok := entryFD.Options().(*descriptorpb.FieldOptions)
	return !ok || entryOpts.GetFeatures().GetUtf8Validation() == descriptorpb.FeatureSet_UTF8_VALIDATION_UNKNOWN
}

func validateEditionFile(file *protogen.File) error {
	ed := file.Proto.GetEdition()
	if !isSupportedEdition(ed) {
		return fmt.Errorf("omnibuf: unsupported protobuf edition %v", ed)
	}
	for i := 0; i < file.Desc.Enums().Len(); i++ {
		if en := file.Desc.Enums().Get(i); en.IsClosed() {
			return fmt.Errorf("omnibuf: CLOSED enums are not supported (%s)", en.FullName())
		}
	}
	for i := 0; i < file.Desc.Extensions().Len(); i++ {
		ext := file.Desc.Extensions().Get(i)
		if !isCustomDescriptorOptionExtension(ext) {
			return fmt.Errorf("omnibuf: extensions are not supported (%s)", ext.FullName())
		}
	}
	visited := make(map[protoreflect.FullName]bool)
	for i := 0; i < file.Desc.Messages().Len(); i++ {
		if err := validateEditionMessage(file.Desc.Messages().Get(i), visited); err != nil {
			return err
		}
	}
	return nil
}

func validateEditionMessage(md protoreflect.MessageDescriptor, visited map[protoreflect.FullName]bool) error {
	if md == nil || visited[md.FullName()] {
		return nil
	}
	visited[md.FullName()] = true

	if md.Syntax() == protoreflect.Editions {
		if pf := md.ParentFile(); pf == nil || !isSupportedEdition(fileEdition(pf)) {
			return fmt.Errorf("omnibuf: unsupported protobuf edition %v (%s)", fileEdition(pf), md.FullName())
		}
		if md.ExtensionRanges().Len() > 0 {
			return fmt.Errorf("omnibuf: extensions are not supported (%s)", md.FullName())
		}
		for i := 0; i < md.Extensions().Len(); i++ {
			ext := md.Extensions().Get(i)
			if !isCustomDescriptorOptionExtension(ext) {
				return fmt.Errorf("omnibuf: extensions are not supported (%s)", ext.FullName())
			}
		}
		for i := 0; i < md.Enums().Len(); i++ {
			if en := md.Enums().Get(i); en.IsClosed() {
				return fmt.Errorf("omnibuf: CLOSED enums are not supported (%s)", en.FullName())
			}
		}
		for i := 0; i < md.Fields().Len(); i++ {
			fd := md.Fields().Get(i)
			if fd.Cardinality() == protoreflect.Required {
				return fmt.Errorf("omnibuf: LEGACY_REQUIRED field presence is not supported (%s)", fd.FullName())
			}
			if fd.Enum() != nil && fd.Enum().IsClosed() {
				return fmt.Errorf("omnibuf: CLOSED enums are not supported (%s)", fd.FullName())
			}
			if fd.HasDefault() && !isZeroDefaultValue(fd) {
				return fmt.Errorf("omnibuf: custom non-zero default value is not supported (%s)", fd.FullName())
			}
			if fd.IsMap() {
				if key := fd.MapKey(); key != nil && key.Kind() == protoreflect.StringKind && mapFieldDisablesUTF8(fd, key) {
					return fmt.Errorf("omnibuf: utf8_validation = NONE is not supported (%s)", fd.FullName())
				}
				if val := fd.MapValue(); val != nil {
					if val.Kind() == protoreflect.StringKind && mapFieldDisablesUTF8(fd, val) {
						return fmt.Errorf("omnibuf: utf8_validation = NONE is not supported (%s)", fd.FullName())
					}
					if val.Enum() != nil && val.Enum().IsClosed() {
						return fmt.Errorf("omnibuf: CLOSED enums are not supported (%s)", fd.FullName())
					}
					if val.HasDefault() && !isZeroDefaultValue(val) {
						return fmt.Errorf("omnibuf: custom non-zero default value is not supported (%s)", fd.FullName())
					}
					if val.Message() != nil {
						if err := validateEditionMessage(val.Message(), visited); err != nil {
							return err
						}
					}
				}
				continue
			}
			if fd.Kind() == protoreflect.StringKind && !fieldEnforcesUTF8(fd) {
				return fmt.Errorf("omnibuf: utf8_validation = NONE is not supported (%s)", fd.FullName())
			}
			if fd.Message() != nil {
				if err := validateEditionMessage(fd.Message(), visited); err != nil {
					return err
				}
			}
		}
	}

	for i := 0; i < md.Messages().Len(); i++ {
		nested := md.Messages().Get(i)
		if nested.IsMapEntry() {
			continue
		}
		if err := validateEditionMessage(nested, visited); err != nil {
			return err
		}
	}
	return nil
}

// Editions delegate to omnibufproto so open, hybrid, and opaque Go APIs share one codec path.
func generateEditionFile(gen *protogen.Plugin, file *protogen.File) {
	if err := validateEditionFile(file); err != nil {
		gen.Error(err)
		return
	}
	g := gen.NewGeneratedFile(file.GeneratedFilenamePrefix+"_wire.pb.go", file.GoImportPath)
	g.P("// Code generated by protoc-gen-omnibuf-go. DO NOT EDIT.")
	g.P("// source: ", file.Desc.Path())
	g.P()
	g.P("package ", file.GoPackageName)
	g.P()
	bridge := protogen.GoImportPath("github.com/omnibuf/omnibuf-go/omnibufproto")
	var visit func(*protogen.Message)
	visit = func(msg *protogen.Message) {
		if msg.Desc.IsMapEntry() {
			return
		}
		name := msg.GoIdent
		g.P("func (x *", name, ") TryMarshalOmniBufAppend(dst []byte) ([]byte, error) {")
		g.P("	return (", bridge.Ident("MarshalOptions"), "{Deterministic: true}).MarshalAppend(dst, x.ProtoReflect())")
		g.P("}")
		g.P()
		g.P("func (x *", name, ") MarshalOmniBufAppend(dst []byte) []byte {")
		g.P("	b, err := x.TryMarshalOmniBufAppend(dst)")
		g.P("	if err != nil {")
		g.P("		panic(err)")
		g.P("	}")
		g.P("	return b")
		g.P("}")
		g.P()
		g.P("func (x *", name, ") TryMarshalOmniBufSelfDescribingAppend(dst []byte) ([]byte, error) {")
		g.P("	return (", bridge.Ident("MarshalOptions"), "{Deterministic: true, SelfDescribing: true}).MarshalAppend(dst, x.ProtoReflect())")
		g.P("}")
		g.P()
		g.P("func (x *", name, ") MarshalOmniBufSelfDescribingAppend(dst []byte) []byte {")
		g.P("	b, err := x.TryMarshalOmniBufSelfDescribingAppend(dst)")
		g.P("	if err != nil {")
		g.P("		panic(err)")
		g.P("	}")
		g.P("	return b")
		g.P("}")
		g.P()
		g.P("func (x *", name, ") UnmarshalOmniBuf(data []byte, zeroCopy bool) error {")
		g.P("	return (", bridge.Ident("UnmarshalOptions"), "{ZeroCopy: zeroCopy}).Unmarshal(data, x.ProtoReflect())")
		g.P("}")
		g.P()
		for _, nested := range msg.Messages {
			visit(nested)
		}
	}
	for _, msg := range file.Messages {
		visit(msg)
	}
}
