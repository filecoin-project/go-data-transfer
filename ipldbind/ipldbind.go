package ipldbind

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"os"
	"reflect"

	"github.com/ipld/go-ipld-prime"
	"github.com/ipld/go-ipld-prime/codec/dagcbor"
	"github.com/ipld/go-ipld-prime/codec/dagjson"
	"github.com/ipld/go-ipld-prime/datamodel"
	"github.com/ipld/go-ipld-prime/node/bindnode"
	"github.com/ipld/go-ipld-prime/schema"
)

var prototype map[string]schema.TypedPrototype = make(map[string]schema.TypedPrototype)

func typeName(ptrValue interface{}) string {
	val := reflect.ValueOf(ptrValue).Type()
	for val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	return val.Name()
}

func RegisterType(schema string, ptrType interface{}) error {
	name := typeName(ptrType)
	if _, ok := prototype[name]; ok {
		return fmt.Errorf("tried to register existing type %v", name)
	}
	typeSystem, err := ipld.LoadSchemaBytes([]byte(schema))
	if err != nil {
		return err
	}
	prototype[name] = bindnode.Prototype(ptrType, typeSystem.TypeByName(name))
	return nil
}

func ToNode(ptrValue interface{}) (datamodel.Node, error) {
	if ptrValue == nil {
		return datamodel.Null, nil
	}
	val := reflect.ValueOf(ptrValue).Type()
	fmt.Printf("EncodeToNode %T: [%v]\n", ptrValue, val.Name())
	node, ok := ptrValue.(datamodel.Node)
	if ok {
		return node, nil
	}

	name := typeName(ptrValue)
	proto, ok := prototype[name]
	if !ok {
		return nil, fmt.Errorf("invalid TypedPrototype: %v", name)
	}
	return bindnode.Wrap(ptrValue, proto.Type()), nil
}

func ToEncoded(ptrValue interface{}) ([]byte, error) {
	node, _ := ToNode(ptrValue)
	buf := &bytes.Buffer{}
	if tn, ok := node.(schema.TypedNode); ok {
		node = tn.Representation()
	}
	err := dagcbor.Encode(node, buf)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func FromNode(node datamodel.Node, ptrValue interface{}) (interface{}, error) {
	name := typeName(ptrValue)
	proto, ok := prototype[name]
	if !ok {
		return nil, fmt.Errorf("invalid TypedPrototype: %v", name)
	}
	if tn, ok := node.(schema.TypedNode); ok {
		node = tn.Representation()
	}
	fmt.Printf("Unwrapping %v\n", name)
	dagjson.Encode(node, os.Stdout)
	fmt.Println()
	builder := proto.Representation().NewBuilder()
	err := builder.AssignNode(node)
	if err != nil {
		return nil, err
	}
	return bindnode.Unwrap(builder.Build()), nil
}

func FromEncoded(r io.Reader, ptrValue interface{}) (interface{}, error) {
	name := typeName(ptrValue)
	proto, ok := prototype[name]
	if !ok {
		return nil, fmt.Errorf("invalid TypedPrototype: %v", name)
	}
	builder := proto.Representation().NewBuilder()
	err := dagcbor.Decode(builder, r)
	if err != nil {
		return nil, err
	}
	node := builder.Build()
	return bindnode.Unwrap(node), nil
}
