// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package protobuf

import (
	"encoding/json"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	schemaDefinition = "schema.definition"
	schemaWeight     = "schema.weight"
	schemaType       = "schema.type"
	schemaId         = "schema.id"
	schemaOperation  = "schema.operation"
	schemaName       = "schema.name"
)

type messageField struct {
	Name        string `json:"name"`
	Number      int32  `json:"number"`
	Cardinality string `json:"cardinality"`
	Kind        string `json:"kind"`
	Message     string `json:"message,omitempty"`
	Enum        string `json:"enum,omitempty"`
}

type messageSchema struct {
	FullName string         `json:"full_name"`
	Fields   []messageField `json:"fields"`
	Syntax   string         `json:"syntax"`
}

type enumValue struct {
	Name   string `json:"name"`
	Number int32  `json:"number"`
}

type enum struct {
	Name   string      `json:"name"`
	Values []enumValue `json:"values"`
}

type schema struct {
	Messages      []messageSchema `json:"messages"`
	Enums         []enum          `json:"enums"`
	ParentMessage string          `json:"parent_message"`
	// Kind is protobuf
	Kind string `json:"kind"`
}

func extractEnum(desc protoreflect.EnumDescriptor, schema *schema, extracted map[string]struct{}) {
	if _, ok := extracted[string(desc.FullName())]; ok {
		return
	}
	extracted[string(desc.FullName())] = struct{}{}

	enum := enum{
		Name:   string(desc.FullName()),
		Values: make([]enumValue, 0, desc.Values().Len()),
	}
	for i := 0; i < desc.Values().Len(); i++ {
		e := desc.Values().Get(i)
		enum.Values = append(enum.Values, enumValue{
			Name:   string(e.Name()),
			Number: int32(e.Number()),
		})
	}
	schema.Enums = append(schema.Enums, enum)
	return
}

func extractMessage(desc protoreflect.MessageDescriptor, schema *schema, extracted map[string]struct{}) {
	if _, ok := extracted[string(desc.FullName())]; ok {
		return
	}
	extracted[string(desc.FullName())] = struct{}{}

	messageSchema := messageSchema{
		FullName: string(desc.FullName()),
		Fields:   make([]messageField, 0, desc.Fields().Len()),
		Syntax:   desc.Syntax().String(),
	}
	for i := 0; i < desc.Fields().Len(); i++ {
		field := desc.Fields().Get(i)
		msgField := messageField{
			Name:        string(field.Name()),
			Number:      int32(field.Number()),
			Cardinality: field.Cardinality().String(),
			Kind:        field.Kind().String(),
		}
		if field.Kind() == protoreflect.MessageKind {
			extractMessage(field.Message(), schema, extracted)
			msgField.Message = string(field.Message().FullName())
		}
		if field.Kind() == protoreflect.EnumKind {
			extractEnum(field.Enum(), schema, extracted)
			msgField.Enum = string(field.Enum().FullName())
		}
		messageSchema.Fields = append(messageSchema.Fields, msgField)
	}
	schema.Messages = append(schema.Messages, messageSchema)
}

func getSchema(m proto.Message) (definition string, name string, err error) {
	descr := m.ProtoReflect().Descriptor()
	extracted := make(map[string]struct{})
	schema := schema{Kind: "protobuf", ParentMessage: string(descr.FullName())}
	extractMessage(descr, &schema, extracted)
	schemaStr, err := json.Marshal(schema)
	if err != nil {
		return "", "", err
	}
	return string(schemaStr), schema.ParentMessage, nil
}
