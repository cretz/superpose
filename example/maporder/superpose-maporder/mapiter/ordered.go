package mapiter

import (
	"sort"

	"golang.org/x/exp/constraints"
)

func PanicUnorderedKeys[K comparable, V any](m map[K]V) map[K]V {
	panic("cannot do safe map iteration on unordered keys")
}

type MapIter[K comparable, V any] struct {
	initialKeys []K
	m           map[K]V
	keyIndex    int
	value       V
}

func NewOrderedIter[K constraints.Ordered, V interface{}](m map[K]V) *MapIter[K, V] {
	ret := &MapIter[K, V]{
		initialKeys: make([]K, 0, len(m)),
		m:           m,
		keyIndex:    -1,
	}
	for k := range m {
		ret.initialKeys = append(ret.initialKeys, k)
	}
	sort.Slice(ret.initialKeys, func(i, j int) bool { return ret.initialKeys[i] < ret.initialKeys[j] })
	return ret
}

func (m *MapIter[K, V]) Next() bool {
	// Loop until we find a value or no more next
	for m.keyIndex+1 < len(m.initialKeys) {
		m.keyIndex++
		var ok bool
		if m.value, ok = m.m[m.initialKeys[m.keyIndex]]; ok {
			return true
		}
	}
	return false
}

func (m *MapIter[K, V]) Key() K {
	if m.keyIndex < 0 {
		panic("next never called")
	} else if m.keyIndex >= len(m.initialKeys) {
		panic("past end of iterator")
	}
	return m.initialKeys[m.keyIndex]
}

func (m *MapIter[K, V]) Value() V {
	if m.keyIndex < 0 {
		panic("next never called")
	} else if m.keyIndex >= len(m.initialKeys) {
		panic("past end of iterator")
	}
	return m.value
}
