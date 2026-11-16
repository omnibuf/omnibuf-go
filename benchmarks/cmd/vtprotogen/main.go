package main

import (
	"flag"
	"fmt"
	"os"

	_ "github.com/planetscale/vtprotobuf/features/marshal"
	_ "github.com/planetscale/vtprotobuf/features/pool"
	_ "github.com/planetscale/vtprotobuf/features/size"
	_ "github.com/planetscale/vtprotobuf/features/unmarshal"
	vtgenerator "github.com/planetscale/vtprotobuf/generator"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

func peerProtoRequest() *pluginpb.CodeGeneratorRequest {
	f := func(name string, num int32, label descriptorpb.FieldDescriptorProto_Label, typ descriptorpb.FieldDescriptorProto_Type, typeName string) *descriptorpb.FieldDescriptorProto {
		fd := &descriptorpb.FieldDescriptorProto{
			Name:   proto.String(name),
			Number: proto.Int32(num),
			Label:  label.Enum(),
			Type:   typ.Enum(),
		}
		if typeName != "" {
			fd.TypeName = proto.String(typeName)
		}
		return fd
	}
	opt := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	rep := descriptorpb.FieldDescriptorProto_LABEL_REPEATED
	fdp := &descriptorpb.FileDescriptorProto{
		Syntax:  proto.String("proto3"),
		Name:    proto.String("omnibuf/bench/peer.proto"),
		Package: proto.String("omnibuf.bench"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("github.com/omnibuf/omnibuf-go/benchmarks/pb;pb"),
		},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("Location"),
				Field: []*descriptorpb.FieldDescriptorProto{
					f("region", 1, opt, descriptorpb.FieldDescriptorProto_TYPE_STRING, ""),
					f("datacenter", 2, opt, descriptorpb.FieldDescriptorProto_TYPE_STRING, ""),
					f("city", 3, opt, descriptorpb.FieldDescriptorProto_TYPE_STRING, ""),
					f("rack", 4, opt, descriptorpb.FieldDescriptorProto_TYPE_UINT32, ""),
					f("latitude", 5, opt, descriptorpb.FieldDescriptorProto_TYPE_FLOAT, ""),
					f("longitude", 6, opt, descriptorpb.FieldDescriptorProto_TYPE_FLOAT, ""),
				},
			},
			{
				Name: proto.String("Neighbor"),
				Field: []*descriptorpb.FieldDescriptorProto{
					f("peer_asn", 1, opt, descriptorpb.FieldDescriptorProto_TYPE_UINT32, ""),
					f("router", 2, opt, descriptorpb.FieldDescriptorProto_TYPE_STRING, ""),
					f("latency_us", 3, opt, descriptorpb.FieldDescriptorProto_TYPE_UINT32, ""),
					f("established", 4, opt, descriptorpb.FieldDescriptorProto_TYPE_BOOL, ""),
				},
			},
			{
				Name: proto.String("PeerUpdate"),
				Field: []*descriptorpb.FieldDescriptorProto{
					f("asn", 1, opt, descriptorpb.FieldDescriptorProto_TYPE_UINT32, ""),
					f("router_id", 2, opt, descriptorpb.FieldDescriptorProto_TYPE_STRING, ""),
					f("active", 3, opt, descriptorpb.FieldDescriptorProto_TYPE_BOOL, ""),
					f("uptime_sec", 4, opt, descriptorpb.FieldDescriptorProto_TYPE_UINT64, ""),
					f("rssi_dbm", 5, opt, descriptorpb.FieldDescriptorProto_TYPE_SINT32, ""),
					f("cpu_load", 6, opt, descriptorpb.FieldDescriptorProto_TYPE_DOUBLE, ""),
					f("tags", 7, rep, descriptorpb.FieldDescriptorProto_TYPE_STRING, ""),
					f("prefixes", 8, rep, descriptorpb.FieldDescriptorProto_TYPE_UINT32, ""),
					f("location", 9, opt, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, ".omnibuf.bench.Location"),
					f("neighbors", 10, rep, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, ".omnibuf.bench.Neighbor"),
					f("sequence", 11, opt, descriptorpb.FieldDescriptorProto_TYPE_UINT64, ""),
				},
			},
		},
	}
	return &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"omnibuf/bench/peer.proto"},
		ProtoFile:      []*descriptorpb.FileDescriptorProto{fdp},
	}
}

func main() {
	outPath := flag.String("out", "pb/peer_vtproto.pb.go", "Output path for generated vtprotobuf file")
	flag.Parse()

	plugin, err := protogen.Options{}.New(peerProtoRequest())
	if err != nil {
		fmt.Fprintf(os.Stderr, "vtprotogen: %v\n", err)
		os.Exit(1)
	}
	cfg := vtgenerator.Config{
		Poolable:        vtgenerator.NewObjectSet(),
		PoolableExclude: vtgenerator.NewObjectSet(),
	}
	_ = cfg.Poolable.Set("github.com/omnibuf/omnibuf-go/benchmarks/pb.*")
	gen, err := vtgenerator.NewGenerator(plugin, []string{"marshal", "size", "unmarshal", "unmarshal_unsafe", "pool"}, &cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vtprotogen: %v\n", err)
		os.Exit(1)
	}
	gen.Generate()
	resp := plugin.Response()
	if resp.Error != nil {
		fmt.Fprintf(os.Stderr, "vtprotogen: %s\n", resp.GetError())
		os.Exit(1)
	}
	for _, rf := range resp.File {
		if err := os.WriteFile(*outPath, []byte(rf.GetContent()), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "vtprotogen: %v\n", err)
			os.Exit(1)
		}
	}
}
