package omnibufproto

import (
	"fmt"
	"math"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

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

func isEditionDelimitedField(fd protoreflect.FieldDescriptor) bool {
	return fd.Kind() == protoreflect.GroupKind && fd.ParentFile() != nil && fd.ParentFile().Syntax() == protoreflect.Editions
}

func isProtoSubMessageField(fd protoreflect.FieldDescriptor) bool {
	return fd.Kind() == protoreflect.MessageKind || isEditionDelimitedField(fd)
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
	// Guard against synthetic descriptors that set utf8_validation = NONE on the
	// map field without copying it onto the map entry key/value field options.
	mapOpts, ok := mapFD.Options().(*descriptorpb.FieldOptions)
	if !ok || mapOpts.GetFeatures().GetUtf8Validation() != descriptorpb.FeatureSet_NONE {
		return false
	}
	entryOpts, ok := entryFD.Options().(*descriptorpb.FieldOptions)
	return !ok || entryOpts.GetFeatures().GetUtf8Validation() == descriptorpb.FeatureSet_UTF8_VALIDATION_UNKNOWN
}

func validateEditionDescriptorTree(md protoreflect.MessageDescriptor) error {
	return validateEditionDescriptorVisited(md, make(map[protoreflect.FullName]bool), make(map[protoreflect.FileDescriptor]bool))
}

func validateEditionDescriptorVisited(md protoreflect.MessageDescriptor, visited map[protoreflect.FullName]bool, checkedFiles map[protoreflect.FileDescriptor]bool) error {
	if md == nil || visited[md.FullName()] {
		return nil
	}
	visited[md.FullName()] = true

	if md.Syntax() == protoreflect.Editions {
		pf := md.ParentFile()
		if pf == nil {
			return fmt.Errorf("omnibuf: unsupported protobuf edition %v (%s)", descriptorpb.Edition_EDITION_UNKNOWN, md.FullName())
		}
		if !checkedFiles[pf] {
			checkedFiles[pf] = true
			if ed := fileEdition(pf); !isSupportedEdition(ed) {
				return fmt.Errorf("omnibuf: unsupported protobuf edition %v (%s)", ed, md.FullName())
			}
			for i := 0; i < pf.Extensions().Len(); i++ {
				ext := pf.Extensions().Get(i)
				if !isCustomDescriptorOptionExtension(ext) {
					return fmt.Errorf("omnibuf: extensions are not supported (%s)", ext.FullName())
				}
			}
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
						if err := validateEditionDescriptorVisited(val.Message(), visited, checkedFiles); err != nil {
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
				if err := validateEditionDescriptorVisited(fd.Message(), visited, checkedFiles); err != nil {
					return err
				}
			}
		}
		return nil
	}

	for i := 0; i < md.Fields().Len(); i++ {
		fd := md.Fields().Get(i)
		if fd.IsMap() {
			if val := fd.MapValue(); val != nil && val.Message() != nil {
				if err := validateEditionDescriptorVisited(val.Message(), visited, checkedFiles); err != nil {
					return err
				}
			}
		} else if fd.Message() != nil {
			if err := validateEditionDescriptorVisited(fd.Message(), visited, checkedFiles); err != nil {
				return err
			}
		}
	}
	return nil
}
