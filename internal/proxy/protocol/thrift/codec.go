package thrift

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/apache/thrift/lib/go/thrift"
	"github.com/cloudwego/thriftgo/parser"
)

// codec handles JSON↔Thrift serialization using TProtocol primitives.
type codec struct {
	structs  map[string]*parser.StructLike
	enums    map[string]*parser.Enum
	typedefs map[string]*parser.Type
}

// jsonToThriftArgs writes JSON data as Thrift struct fields (the args struct of an RPC call).
func (c *codec) jsonToThriftArgs(ctx context.Context, oprot thrift.TProtocol, args []*parser.Field, jsonData map[string]interface{}) error {
	if err := oprot.WriteStructBegin(ctx, "args"); err != nil {
		return err
	}
	for _, field := range args {
		val, ok := jsonData[field.Name]
		if !ok {
			continue // skip missing optional fields
		}
		resolvedType := c.resolveType(field.Type)
		tt := categoryToTType(resolvedType.Category)
		if err := oprot.WriteFieldBegin(ctx, field.Name, tt, int16(field.ID)); err != nil {
			return err
		}
		if err := c.writeValue(ctx, oprot, field.Type, val); err != nil {
			return fmt.Errorf("field %s: %w", field.Name, err)
		}
		if err := oprot.WriteFieldEnd(ctx); err != nil {
			return err
		}
	}
	if err := oprot.WriteFieldStop(ctx); err != nil {
		return err
	}
	return oprot.WriteStructEnd(ctx)
}

// writeValue dispatches on IDL type to write a value via TProtocol.
func (c *codec) writeValue(ctx context.Context, oprot thrift.TProtocol, idlType *parser.Type, val interface{}) error {
	resolved := c.resolveType(idlType)
	cat := resolved.Category

	switch {
	case cat.IsBool():
		b, ok := val.(bool)
		if !ok {
			return fmt.Errorf("expected bool, got %T", val)
		}
		return oprot.WriteBool(ctx, b)

	case cat.IsByte():
		n, err := toInt64(val)
		if err != nil {
			return fmt.Errorf("expected byte: %w", err)
		}
		return oprot.WriteByte(ctx, int8(n))

	case cat.IsI16():
		n, err := toInt64(val)
		if err != nil {
			return fmt.Errorf("expected i16: %w", err)
		}
		return oprot.WriteI16(ctx, int16(n))

	case cat.IsI32():
		n, err := toInt64(val)
		if err != nil {
			return fmt.Errorf("expected i32: %w", err)
		}
		return oprot.WriteI32(ctx, int32(n))

	case cat.IsI64():
		n, err := toInt64(val)
		if err != nil {
			return fmt.Errorf("expected i64: %w", err)
		}
		return oprot.WriteI64(ctx, n)

	case cat.IsDouble():
		f, err := toFloat64(val)
		if err != nil {
			return fmt.Errorf("expected double: %w", err)
		}
		return oprot.WriteDouble(ctx, f)

	case cat.IsString():
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("expected string, got %T", val)
		}
		return oprot.WriteString(ctx, s)

	case cat.IsBinary():
		switch v := val.(type) {
		case string:
			data, err := base64.StdEncoding.DecodeString(v)
			if err != nil {
				return fmt.Errorf("binary field: invalid base64: %w", err)
			}
			return oprot.WriteBinary(ctx, data)
		case []byte:
			return oprot.WriteBinary(ctx, v)
		default:
			return fmt.Errorf("expected string (base64) or bytes for binary, got %T", val)
		}

	case cat.IsEnum():
		return c.writeEnum(ctx, oprot, resolved, val)

	case cat.IsStruct() || cat.IsUnion() || cat.IsException():
		m, ok := val.(map[string]interface{})
		if !ok {
			return fmt.Errorf("expected object for struct %s, got %T", resolved.Name, val)
		}
		return c.writeStruct(ctx, oprot, resolved.Name, m)

	case cat.IsList():
		return c.writeList(ctx, oprot, resolved, val)

	case cat.IsSet():
		return c.writeSet(ctx, oprot, resolved, val)

	case cat.IsMap():
		return c.writeMap(ctx, oprot, resolved, val)

	default:
		return fmt.Errorf("unsupported thrift type category: %s", cat.String())
	}
}

func (c *codec) writeEnum(ctx context.Context, oprot thrift.TProtocol, resolved *parser.Type, val interface{}) error {
	switch v := val.(type) {
	case float64:
		return oprot.WriteI32(ctx, int32(v))
	case string:
		// Look up enum value by name.
		e, ok := c.enums[resolved.Name]
		if !ok {
			return fmt.Errorf("unknown enum type: %s", resolved.Name)
		}
		for _, ev := range e.Values {
			if ev.Name == v {
				return oprot.WriteI32(ctx, int32(ev.Value))
			}
		}
		return fmt.Errorf("unknown enum value %q for %s", v, resolved.Name)
	default:
		n, err := toInt64(val)
		if err != nil {
			return fmt.Errorf("expected number or string for enum: %w", err)
		}
		return oprot.WriteI32(ctx, int32(n))
	}
}

func (c *codec) writeStruct(ctx context.Context, oprot thrift.TProtocol, structName string, data map[string]interface{}) error {
	sd, ok := c.structs[structName]
	if !ok {
		return fmt.Errorf("unknown struct type: %s", structName)
	}

	if err := oprot.WriteStructBegin(ctx, structName); err != nil {
		return err
	}
	for _, field := range sd.Fields {
		val, ok := data[field.Name]
		if !ok {
			continue
		}
		resolved := c.resolveType(field.Type)
		tt := categoryToTType(resolved.Category)
		if err := oprot.WriteFieldBegin(ctx, field.Name, tt, int16(field.ID)); err != nil {
			return err
		}
		if err := c.writeValue(ctx, oprot, field.Type, val); err != nil {
			return fmt.Errorf("struct %s field %s: %w", structName, field.Name, err)
		}
		if err := oprot.WriteFieldEnd(ctx); err != nil {
			return err
		}
	}
	if err := oprot.WriteFieldStop(ctx); err != nil {
		return err
	}
	return oprot.WriteStructEnd(ctx)
}

func (c *codec) writeList(ctx context.Context, oprot thrift.TProtocol, resolved *parser.Type, val interface{}) error {
	items, ok := val.([]interface{})
	if !ok {
		return fmt.Errorf("expected array for list, got %T", val)
	}
	elemResolved := c.resolveType(resolved.ValueType)
	elemTType := categoryToTType(elemResolved.Category)
	if err := oprot.WriteListBegin(ctx, elemTType, len(items)); err != nil {
		return err
	}
	for i, item := range items {
		if err := c.writeValue(ctx, oprot, resolved.ValueType, item); err != nil {
			return fmt.Errorf("list[%d]: %w", i, err)
		}
	}
	return oprot.WriteListEnd(ctx)
}

func (c *codec) writeSet(ctx context.Context, oprot thrift.TProtocol, resolved *parser.Type, val interface{}) error {
	items, ok := val.([]interface{})
	if !ok {
		return fmt.Errorf("expected array for set, got %T", val)
	}
	elemResolved := c.resolveType(resolved.ValueType)
	elemTType := categoryToTType(elemResolved.Category)
	if err := oprot.WriteSetBegin(ctx, elemTType, len(items)); err != nil {
		return err
	}
	for i, item := range items {
		if err := c.writeValue(ctx, oprot, resolved.ValueType, item); err != nil {
			return fmt.Errorf("set[%d]: %w", i, err)
		}
	}
	return oprot.WriteSetEnd(ctx)
}

func (c *codec) writeMap(ctx context.Context, oprot thrift.TProtocol, resolved *parser.Type, val interface{}) error {
	m, ok := val.(map[string]interface{})
	if !ok {
		return fmt.Errorf("expected object for map, got %T", val)
	}
	keyResolved := c.resolveType(resolved.KeyType)
	valResolved := c.resolveType(resolved.ValueType)
	keyTType := categoryToTType(keyResolved.Category)
	valTType := categoryToTType(valResolved.Category)
	if err := oprot.WriteMapBegin(ctx, keyTType, valTType, len(m)); err != nil {
		return err
	}
	for k, v := range m {
		// Map keys in JSON are always strings; convert to the declared key type.
		keyVal, err := convertStringKey(k, keyResolved)
		if err != nil {
			return fmt.Errorf("map key %q: %w", k, err)
		}
		if err := c.writeValue(ctx, oprot, resolved.KeyType, keyVal); err != nil {
			return fmt.Errorf("map key: %w", err)
		}
		if err := c.writeValue(ctx, oprot, resolved.ValueType, v); err != nil {
			return fmt.Errorf("map value: %w", err)
		}
	}
	return oprot.WriteMapEnd(ctx)
}

// readResult reads the Thrift reply struct and returns JSON bytes.
// Field 0 = success value, fields 1+ = declared exceptions.
func (c *codec) readResult(ctx context.Context, iprot thrift.TProtocol, ms *methodSchema) ([]byte, error) {
	if _, err := iprot.ReadStructBegin(ctx); err != nil {
		return nil, err
	}

	var result interface{}
	var exceptionName string

	for {
		_, fieldType, fieldID, err := iprot.ReadFieldBegin(ctx)
		if err != nil {
			return nil, err
		}
		if fieldType == thrift.STOP {
			break
		}

		if fieldID == 0 {
			// Success return value.
			if ms.void {
				if err := iprot.Skip(ctx, fieldType); err != nil {
					return nil, err
				}
			} else {
				result, err = c.readValue(ctx, iprot, ms.returnType)
				if err != nil {
					return nil, fmt.Errorf("reading return value: %w", err)
				}
			}
		} else {
			// Exception field — find it in throws.
			var throwType *parser.Type
			for _, t := range ms.throws {
				if int16(t.ID) == fieldID {
					throwType = t.Type
					exceptionName = t.Name
					break
				}
			}
			if throwType != nil {
				result, err = c.readValue(ctx, iprot, throwType)
				if err != nil {
					return nil, fmt.Errorf("reading exception: %w", err)
				}
			} else {
				if err := iprot.Skip(ctx, fieldType); err != nil {
					return nil, err
				}
			}
		}
		if err := iprot.ReadFieldEnd(ctx); err != nil {
			return nil, err
		}
	}

	if err := iprot.ReadStructEnd(ctx); err != nil {
		return nil, err
	}

	if exceptionName != "" {
		return nil, fmt.Errorf("thrift exception %s: %v", exceptionName, result)
	}

	if ms.void && result == nil {
		return []byte("{}"), nil
	}

	return marshalJSON(result)
}

// readValue reads a Thrift value and returns a Go interface{}.
func (c *codec) readValue(ctx context.Context, iprot thrift.TProtocol, idlType *parser.Type) (interface{}, error) {
	resolved := c.resolveType(idlType)
	cat := resolved.Category

	switch {
	case cat.IsBool():
		return iprot.ReadBool(ctx)

	case cat.IsByte():
		v, err := iprot.ReadByte(ctx)
		return float64(v), err

	case cat.IsI16():
		v, err := iprot.ReadI16(ctx)
		return float64(v), err

	case cat.IsI32():
		v, err := iprot.ReadI32(ctx)
		return float64(v), err

	case cat.IsI64():
		v, err := iprot.ReadI64(ctx)
		// Use float64 for JSON compatibility unless it exceeds safe integer range.
		if v > (1<<53) || v < -(1<<53) {
			return v, err
		}
		return float64(v), err

	case cat.IsDouble():
		return iprot.ReadDouble(ctx)

	case cat.IsString():
		return iprot.ReadString(ctx)

	case cat.IsBinary():
		data, err := iprot.ReadBinary(ctx)
		if err != nil {
			return nil, err
		}
		return base64.StdEncoding.EncodeToString(data), nil

	case cat.IsEnum():
		v, err := iprot.ReadI32(ctx)
		if err != nil {
			return nil, err
		}
		// Try to resolve to enum name.
		if e, ok := c.enums[resolved.Name]; ok {
			for _, ev := range e.Values {
				if int32(ev.Value) == v {
					return ev.Name, nil
				}
			}
		}
		return float64(v), nil

	case cat.IsStruct() || cat.IsUnion() || cat.IsException():
		return c.readStruct(ctx, iprot, resolved.Name)

	case cat.IsList():
		return c.readList(ctx, iprot, resolved)

	case cat.IsSet():
		return c.readSet(ctx, iprot, resolved)

	case cat.IsMap():
		return c.readMap(ctx, iprot, resolved)

	default:
		return nil, fmt.Errorf("unsupported thrift type category: %s", cat.String())
	}
}

// readStruct reads a Thrift struct by field ID lookup.
func (c *codec) readStruct(ctx context.Context, iprot thrift.TProtocol, structName string) (map[string]interface{}, error) {
	sd, ok := c.structs[structName]
	if !ok {
		// Unknown struct — skip all fields.
		return c.readUnknownStruct(ctx, iprot)
	}

	fieldByID := make(map[int16]*parser.Field, len(sd.Fields))
	for _, f := range sd.Fields {
		fieldByID[int16(f.ID)] = f
	}

	if _, err := iprot.ReadStructBegin(ctx); err != nil {
		return nil, err
	}

	result := make(map[string]interface{})
	for {
		_, fieldType, fieldID, err := iprot.ReadFieldBegin(ctx)
		if err != nil {
			return nil, err
		}
		if fieldType == thrift.STOP {
			break
		}

		if field, ok := fieldByID[fieldID]; ok {
			val, err := c.readValue(ctx, iprot, field.Type)
			if err != nil {
				return nil, fmt.Errorf("struct %s field %s: %w", structName, field.Name, err)
			}
			result[field.Name] = val
		} else {
			if err := iprot.Skip(ctx, fieldType); err != nil {
				return nil, err
			}
		}

		if err := iprot.ReadFieldEnd(ctx); err != nil {
			return nil, err
		}
	}

	if err := iprot.ReadStructEnd(ctx); err != nil {
		return nil, err
	}

	return result, nil
}

func (c *codec) readUnknownStruct(ctx context.Context, iprot thrift.TProtocol) (map[string]interface{}, error) {
	if _, err := iprot.ReadStructBegin(ctx); err != nil {
		return nil, err
	}
	for {
		_, fieldType, _, err := iprot.ReadFieldBegin(ctx)
		if err != nil {
			return nil, err
		}
		if fieldType == thrift.STOP {
			break
		}
		if err := iprot.Skip(ctx, fieldType); err != nil {
			return nil, err
		}
		if err := iprot.ReadFieldEnd(ctx); err != nil {
			return nil, err
		}
	}
	if err := iprot.ReadStructEnd(ctx); err != nil {
		return nil, err
	}
	return make(map[string]interface{}), nil
}

func (c *codec) readList(ctx context.Context, iprot thrift.TProtocol, resolved *parser.Type) ([]interface{}, error) {
	_, size, err := iprot.ReadListBegin(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]interface{}, 0, size)
	for i := 0; i < size; i++ {
		val, err := c.readValue(ctx, iprot, resolved.ValueType)
		if err != nil {
			return nil, fmt.Errorf("list[%d]: %w", i, err)
		}
		items = append(items, val)
	}
	return items, iprot.ReadListEnd(ctx)
}

func (c *codec) readSet(ctx context.Context, iprot thrift.TProtocol, resolved *parser.Type) ([]interface{}, error) {
	_, size, err := iprot.ReadSetBegin(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]interface{}, 0, size)
	for i := 0; i < size; i++ {
		val, err := c.readValue(ctx, iprot, resolved.ValueType)
		if err != nil {
			return nil, fmt.Errorf("set[%d]: %w", i, err)
		}
		items = append(items, val)
	}
	return items, iprot.ReadSetEnd(ctx)
}

func (c *codec) readMap(ctx context.Context, iprot thrift.TProtocol, resolved *parser.Type) (map[string]interface{}, error) {
	_, _, size, err := iprot.ReadMapBegin(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string]interface{}, size)
	for i := 0; i < size; i++ {
		keyVal, err := c.readValue(ctx, iprot, resolved.KeyType)
		if err != nil {
			return nil, fmt.Errorf("map key [%d]: %w", i, err)
		}
		valVal, err := c.readValue(ctx, iprot, resolved.ValueType)
		if err != nil {
			return nil, fmt.Errorf("map val [%d]: %w", i, err)
		}
		result[fmt.Sprintf("%v", keyVal)] = valVal
	}
	return result, iprot.ReadMapEnd(ctx)
}

// resolveType chases typedefs to the base type.
func (c *codec) resolveType(t *parser.Type) *parser.Type {
	if t == nil {
		return t
	}
	if t.Category.IsTypedef() {
		if base, ok := c.typedefs[t.Name]; ok {
			return c.resolveType(base)
		}
	}
	return t
}

// categoryToTType maps parser Category to thrift.TType.
func categoryToTType(cat parser.Category) thrift.TType {
	switch {
	case cat.IsBool():
		return thrift.BOOL
	case cat.IsByte():
		return thrift.BYTE
	case cat.IsI16():
		return thrift.I16
	case cat.IsI32():
		return thrift.I32
	case cat.IsI64():
		return thrift.I64
	case cat.IsDouble():
		return thrift.DOUBLE
	case cat.IsString():
		return thrift.STRING
	case cat.IsBinary():
		return thrift.STRING // Thrift binary is wire-type STRING
	case cat.IsEnum():
		return thrift.I32
	case cat.IsStruct(), cat.IsUnion(), cat.IsException():
		return thrift.STRUCT
	case cat.IsList():
		return thrift.LIST
	case cat.IsSet():
		return thrift.SET
	case cat.IsMap():
		return thrift.MAP
	default:
		return thrift.STOP
	}
}

// convertStringKey converts a JSON string map key to the appropriate Go type.
func convertStringKey(s string, resolved *parser.Type) (interface{}, error) {
	cat := resolved.Category
	switch {
	case cat.IsString():
		return s, nil
	case cat.IsI32(), cat.IsI16(), cat.IsByte(), cat.IsI64():
		var n int64
		_, err := fmt.Sscanf(s, "%d", &n)
		if err != nil {
			return nil, fmt.Errorf("cannot parse %q as integer: %w", s, err)
		}
		return float64(n), nil
	case cat.IsDouble():
		var f float64
		_, err := fmt.Sscanf(s, "%f", &f)
		if err != nil {
			return nil, fmt.Errorf("cannot parse %q as double: %w", s, err)
		}
		return f, nil
	case cat.IsBool():
		if s == "true" {
			return true, nil
		}
		if s == "false" {
			return false, nil
		}
		return nil, fmt.Errorf("cannot parse %q as bool", s)
	default:
		return s, nil
	}
}

// toInt64 converts a JSON number to int64.
func toInt64(val interface{}) (int64, error) {
	switch v := val.(type) {
	case float64:
		return int64(v), nil
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	default:
		return 0, fmt.Errorf("cannot convert %T to int64", val)
	}
}

// toFloat64 converts a JSON number to float64.
func toFloat64(val interface{}) (float64, error) {
	switch v := val.(type) {
	case float64:
		return v, nil
	case int64:
		return float64(v), nil
	case int:
		return float64(v), nil
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", val)
	}
}

// marshalJSON converts a Go value to JSON bytes.
func marshalJSON(val interface{}) ([]byte, error) {
	if val == nil {
		return []byte("null"), nil
	}
	return json.Marshal(val)
}
