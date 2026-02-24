package config

import (
	"reflect"
	"time"
)

// MergeNonZero returns a copy of base with every non-zero field in overlay
// applied on top. It handles strings (non-empty overrides), ints/floats
// (non-zero overrides), durations (non-zero overrides), bools (always
// override from overlay), slices (non-nil/non-empty overrides), maps
// (merged: base keys + overlay keys, overlay wins), and nested structs
// (recursed). Pointer fields override when non-nil.
//
// This is only called during config load/reload, never per-request.
func MergeNonZero[T any](base, overlay T) T {
	result := base
	mergeValue(reflect.ValueOf(&result).Elem(), reflect.ValueOf(&overlay).Elem())
	return result
}

func mergeValue(dst, src reflect.Value) {
	switch dst.Kind() {
	case reflect.Struct:
		mergeStruct(dst, src)
	case reflect.Map:
		mergeMap(dst, src)
	default:
		if !src.IsZero() {
			dst.Set(src)
		}
	}
}

func mergeStruct(dst, src reflect.Value) {
	t := dst.Type()
	for i := 0; i < t.NumField(); i++ {
		df := dst.Field(i)
		sf := src.Field(i)
		if !df.CanSet() {
			continue
		}

		switch df.Kind() {
		case reflect.Bool:
			// Bools always override from overlay
			df.SetBool(sf.Bool())

		case reflect.Struct:
			// Check for time.Duration (which is int64 underneath but
			// stored inside a struct wrapper in some configs).
			if df.Type() == reflect.TypeOf(time.Duration(0)) {
				if sf.Int() != 0 {
					df.Set(sf)
				}
			} else {
				mergeStruct(df, sf)
			}

		case reflect.Map:
			mergeMap(df, sf)

		case reflect.Ptr:
			if !sf.IsNil() {
				df.Set(sf)
			}

		case reflect.Slice:
			if sf.Len() > 0 {
				df.Set(sf)
			}

		default:
			// String, Int, Float, etc.
			if !sf.IsZero() {
				df.Set(sf)
			}
		}
	}
}

func mergeMap(dst, src reflect.Value) {
	if src.IsNil() || src.Len() == 0 {
		return
	}
	if dst.IsNil() {
		dst.Set(reflect.MakeMap(dst.Type()))
	} else {
		// Copy existing dst into a new map to avoid mutating base
		newMap := reflect.MakeMap(dst.Type())
		for _, k := range dst.MapKeys() {
			newMap.SetMapIndex(k, dst.MapIndex(k))
		}
		dst.Set(newMap)
	}
	for _, k := range src.MapKeys() {
		dst.SetMapIndex(k, src.MapIndex(k))
	}
}
