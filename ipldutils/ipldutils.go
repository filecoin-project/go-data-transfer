package shared

import (
	"bytes"
	"fmt"
	"io"
	"reflect"

	"github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/codec/dagcbor"
	"github.com/ipld/go-ipld-prime/datamodel"
	"github.com/ipld/go-ipld-prime/node/bindnode"
	"github.com/ipld/go-ipld-prime/schema"
	cbg "github.com/whyrusleeping/cbor-gen"
)

type typeWithBindnodeSchema interface {
	BindnodeSchema() string
}

// TODO: remove this I think
type typeWithBindnodePostDecode interface {
	BindnodePostDecode() error
}

// We use the prototype map to store TypedPrototype and Type information
// mapped against Go type names so we only have to run the schema parse once.
// Currently there's not much additional benefit of storing this but there
// may be in the future.
var prototype map[string]schema.TypedPrototype = make(map[string]schema.TypedPrototype)

var bindnodeOptions = []bindnode.Option{}

func typeName(ptrValue interface{}) string {
	val := reflect.ValueOf(ptrValue).Type()
	for val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	return val.Name()
}

// lookup of cached TypedPrototype (and therefore Type) for a Go type, if not
// found, initial parse and setup and caching of the TypedPrototype will happen
func prototypeFor(typeName string, ptrType interface{}) (schema.TypedPrototype, error) {
	proto, ok := prototype[typeName]
	if !ok {
		schemaType, err := schemaTypeFor(typeName, ptrType)
		if err != nil {
			return nil, err
		}
		if schemaType == nil {
			return nil, fmt.Errorf("could not find type [%s] in schema", typeName)
		}
		proto = bindnode.Prototype(ptrType, schemaType, bindnodeOptions...)
		prototype[typeName] = proto
	}
	return proto, nil
}

// load the schema for a Go type, which must have a BindnodeSchema() method
// attached to it
func schemaTypeFor(typeName string, ptrType interface{}) (schema.Type, error) {
	tws, ok := ptrType.(typeWithBindnodeSchema)
	if !ok {
		return nil, fmt.Errorf("attempted to perform IPLD mapping on type without BindnodeSchema(): %T", ptrType)
	}
	schema := tws.BindnodeSchema()
	typeSystem, err := ipld.LoadSchemaBytes([]byte(schema))
	if err != nil {
		return nil, err
	}
	schemaType := typeSystem.TypeByName(typeName)
	if schemaType == nil {
		if !ok {
			return nil, fmt.Errorf("schema for [%T] does not contain that named type [%s]", ptrType, typeName)
		}
	}
	return schemaType, nil
}

// FromReader deserializes DAG-CBOR from a Reader and instantiates the Go type
// that's provided as a pointer via the ptrValue argument.
func FromReader(r io.Reader, ptrValue interface{}) (interface{}, error) {
	name := typeName(ptrValue)
	proto, err := prototypeFor(name, ptrValue)
	if err != nil {
		return nil, err
	}
	node, err := ipld.DecodeStreamingUsingPrototype(r, dagcbor.Decode, proto)
	if err != nil {
		return nil, err
	}
	typ := bindnode.Unwrap(node)
	if twpd, ok := typ.(typeWithBindnodePostDecode); ok {
		// we have some more work to do
		if err = twpd.BindnodePostDecode(); err != nil {
			return nil, err
		}
	}
	return typ, nil
}

// FromNode converts an datamodel.Node into an appropriate Go type that's provided as
// a pointer via the ptrValue argument
func FromNode(node datamodel.Node, ptrValue interface{}) (interface{}, error) {
	name := typeName(ptrValue)
	proto, err := prototypeFor(name, ptrValue)
	if err != nil {
		return nil, err
	}
	if tn, ok := node.(schema.TypedNode); ok {
		node = tn.Representation()
	}
	builder := proto.Representation().NewBuilder()
	err = builder.AssignNode(node)
	if err != nil {
		return nil, err
	}
	typ := bindnode.Unwrap(builder.Build())
	if twpd, ok := typ.(typeWithBindnodePostDecode); ok {
		// we have some more work to do
		if err = twpd.BindnodePostDecode(); err != nil {
			return nil, err
		}
	}
	return typ, nil
}

// ToNode converts a Go type that's provided as a pointer via the ptrValue
// argument to an datamodel.Node.
func ToNode(ptrValue interface{}) (schema.TypedNode, error) {
	name := typeName(ptrValue)
	proto, err := prototypeFor(name, ptrValue)
	if err != nil {
		return nil, err
	}
	return bindnode.Wrap(ptrValue, proto.Type(), bindnodeOptions...), err
}

// NodeToWriter is a utility method that serializes an datamodel.Node as DAG-CBOR to
// a Writer
func NodeToWriter(node datamodel.Node, w io.Writer) error {
	return ipld.EncodeStreaming(w, node, dagcbor.Encode)
}

// NodeToBytes is a utility method that serializes an datamodel.Node as DAG-CBOR to
// a []byte
func NodeToBytes(node datamodel.Node) ([]byte, error) {
	var buf bytes.Buffer
	err := NodeToWriter(node, &buf)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// NodeFromBytes is a utility method that deserializes an untyped datamodel.Node
// from DAG-CBOR format bytes
func NodeFromBytes(b []byte) (datamodel.Node, error) {
	return ipld.Decode(b, dagcbor.Decode)
}

// TypeToWriter is a utility method that serializes a Go type that's provided as a
// pointer via the ptrValue argument as DAG-CBOR to a Writer
func TypeToWriter(ptrValue interface{}, w io.Writer) error {
	node, err := ToNode(ptrValue)
	if err != nil {
		return err
	}
	return ipld.EncodeStreaming(w, node, dagcbor.Encode)
}

func NodeToDeferred(node datamodel.Node) (*cbg.Deferred, error) {
	byts, err := NodeToBytes(node)
	if err != nil {
		return nil, err
	}
	return &cbg.Deferred{Raw: byts}, nil
}

func DeferredToNode(def *cbg.Deferred) (datamodel.Node, error) {
	return NodeFromBytes(def.Raw)
}
