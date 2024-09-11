package duckdb

/*
#include <duckdb.h>
*/
import "C"

import (
	"math/big"
	"time"
	"unsafe"
)

// secondsPerDay to calculate the days since 1970-01-01.
const secondsPerDay = 24 * 60 * 60

// fnSetVectorValue is the setter callback function for any (nested) vector.
type fnSetVectorValue func(vec *vector, rowIdx C.idx_t, val any)

func (vec *vector) setNull(rowIdx C.idx_t) {
	C.duckdb_validity_set_row_invalid(vec.mask, rowIdx)

	if vec.t == TYPE_STRUCT {
		for i := 0; i < len(vec.childVectors); i++ {
			vec.childVectors[i].setNull(rowIdx)
		}
	}
}

func setPrimitive[T any](vec *vector, rowIdx C.idx_t, v T) {
	xs := (*[1 << 31]T)(vec.ptr)
	xs[rowIdx] = v
}

func (vec *vector) setTS(t Type, rowIdx C.idx_t, val any) {
	v := val.(time.Time)
	var ticks int64
	switch t {
	case TYPE_TIMESTAMP:
		ticks = v.UTC().UnixMicro()
	case TYPE_TIMESTAMP_S:
		ticks = v.UTC().Unix()
	case TYPE_TIMESTAMP_MS:
		ticks = v.UTC().UnixMilli()
	case TYPE_TIMESTAMP_NS:
		ticks = v.UTC().UnixNano()
	case TYPE_TIMESTAMP_TZ:
		ticks = v.UTC().UnixMicro()
	}

	var ts C.duckdb_timestamp
	ts.micros = C.int64_t(ticks)
	setPrimitive(vec, rowIdx, ts)
}

func (vec *vector) setDate(rowIdx C.idx_t, val any) {
	// Days since 1970-01-01.
	v := val.(time.Time)
	days := int32(v.UTC().Unix() / secondsPerDay)

	var date C.duckdb_date
	date.days = C.int32_t(days)
	setPrimitive(vec, rowIdx, date)
}

func (vec *vector) setTime(rowIdx C.idx_t, val any) {
	v := val.(time.Time)
	ticks := v.UTC().UnixMicro()

	var t C.duckdb_time
	t.micros = C.int64_t(ticks)
	setPrimitive(vec, rowIdx, t)
}

func (vec *vector) setInterval(rowIdx C.idx_t, val any) {
	v := val.(Interval)
	var interval C.duckdb_interval
	interval.days = C.int32_t(v.Days)
	interval.months = C.int32_t(v.Months)
	interval.micros = C.int64_t(v.Micros)
	setPrimitive(vec, rowIdx, interval)
}

func (vec *vector) setHugeint(rowIdx C.idx_t, val any) {
	v := val.(*big.Int)
	hugeInt, _ := hugeIntFromNative(v)
	setPrimitive(vec, rowIdx, hugeInt)
}

func (vec *vector) setCString(rowIdx C.idx_t, val any) {
	var str string
	if vec.t == TYPE_VARCHAR {
		str = val.(string)
	} else if vec.t == TYPE_BLOB {
		str = string(val.([]byte)[:])
	}

	// This setter also writes BLOBs.
	cStr := C.CString(str)
	C.duckdb_vector_assign_string_element_len(vec.duckdbVector, rowIdx, cStr, C.idx_t(len(str)))
	C.duckdb_free(unsafe.Pointer(cStr))
}

func (vec *vector) setDecimal(t Type, rowIdx C.idx_t, val any) {
	v := val.(Decimal)

	switch t {
	case TYPE_SMALLINT:
		setPrimitive(vec, rowIdx, int16(v.Value.Int64()))
	case TYPE_INTEGER:
		setPrimitive(vec, rowIdx, int32(v.Value.Int64()))
	case TYPE_BIGINT:
		setPrimitive(vec, rowIdx, v.Value.Int64())
	case TYPE_HUGEINT:
		value, _ := hugeIntFromNative(v.Value)
		setPrimitive(vec, rowIdx, value)
	}
}

func (vec *vector) setEnum(t Type, rowIdx C.idx_t, val any) {
	v := vec.dict[val.(string)]

	switch t {
	case TYPE_UTINYINT:
		setPrimitive(vec, rowIdx, uint8(v))
	case TYPE_USMALLINT:
		setPrimitive(vec, rowIdx, uint16(v))
	case TYPE_UINTEGER:
		setPrimitive(vec, rowIdx, v)
	case TYPE_UBIGINT:
		setPrimitive(vec, rowIdx, uint64(v))
	}
}

func (vec *vector) setList(rowIdx C.idx_t, val any) {
	list := val.([]any)
	childVectorSize := C.duckdb_list_vector_get_size(vec.duckdbVector)

	// Set the offset and length of the list vector using the current size of the child vector.
	listEntry := C.duckdb_list_entry{
		offset: C.idx_t(childVectorSize),
		length: C.idx_t(len(list)),
	}
	setPrimitive(vec, rowIdx, listEntry)

	newLength := C.idx_t(len(list)) + childVectorSize
	C.duckdb_list_vector_set_size(vec.duckdbVector, newLength)
	C.duckdb_list_vector_reserve(vec.duckdbVector, newLength)

	// Insert the values into the child vector.
	childVector := &vec.childVectors[0]
	for i, entry := range list {
		offset := C.idx_t(i) + childVectorSize
		childVector.setFn(childVector, offset, entry)
	}
}

func (vec *vector) setStruct(rowIdx C.idx_t, val any) {
	m := val.(map[string]any)
	for i := 0; i < len(vec.childVectors); i++ {
		child := &vec.childVectors[i]
		name := vec.names[i]
		child.setFn(child, rowIdx, m[name])
	}
}

func (vec *vector) setMap(rowIdx C.idx_t, val any) {
	m := val.(Map)

	// Create a LIST of STRUCT values.
	i := 0
	list := make([]any, len(m))
	for key, value := range m {
		list[i] = map[string]any{mapKeysField(): key, mapValuesField(): value}
		i++
	}

	vec.setList(rowIdx, list)
}
