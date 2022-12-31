package mapiter

import (
	"math"
	"reflect"
	"runtime"
	"sort"
	"sync"
)

const reindexKeysAfterCounter = math.MaxInt / 2
const reindexKeysAfterGap = 1000

// Keyed by weak pointer
var insertionMaps = map[uintptr]any{}
var insertionMapsLock sync.RWMutex

type insertionMap[K comparable, V any] struct {
	keyCounter int
	keyIndices map[K]int
	// We accept the performance penalty of locking access to key indices. Go
	// disallows concurrent map access, but this ensures that any Go errors for
	// concurrent map access are on their map and not ours. If we were more
	// performance conscious we could use a skip list maybe.
	keyIndicesLock sync.RWMutex
}

type lit[M ~map[K]V, K comparable, V any] map[K]V

func NewTrackedMapLit[M ~map[K]V, K comparable, V any](size int) lit[M, K, V] {
	return lit[M, K, V](MakeTrackedMap[M](size))
}

func (l lit[M, K, V]) Put(k K, v V) lit[M, K, V] {
	TrackedPut(l, k, v)
	return l
}

func (l lit[M, K, V]) Done() M {
	return map[K]V(l)
}

func MakeTrackedMap[M ~map[K]V, K comparable, V any](size int) M {
	return TrackMap(make(M, size))
}

func TrackMap[K comparable, V any](m map[K]V) map[K]V {
	// Take pointer and add finalizer to remove
	im := &insertionMap[K, V]{}
	ptr := reflect.ValueOf(m).UnsafeAddr()
	insertionMapsLock.Lock()
	insertionMaps[ptr] = im
	insertionMapsLock.Unlock()
	runtime.SetFinalizer(m, func(map[K]V) {
		insertionMapsLock.Lock()
		delete(insertionMaps, ptr)
		insertionMapsLock.Unlock()
	})
	return m
}

func TrackedPut[K comparable, V any](m map[K]V, k K, v V) struct{} {
	getInsertionMap(m).put(m, k, v)
	// We need to return a value in cases like multi-assign
	return struct{}{}
}

func TrackedDelete[K comparable, V any](m map[K]V, k K) {
	getInsertionMap(m).delete(m, k)
}

func TrackedIter[K comparable, V any](m map[K]V) *MapIter[K, V] {
	return getInsertionMap(m).iter(m)
}

func getInsertionMap[K comparable, V any](m map[K]V) *insertionMap[K, V] {
	insertionMapsLock.RLock()
	im := insertionMaps[reflect.ValueOf(m).UnsafeAddr()]
	insertionMapsLock.RUnlock()
	if im == nil {
		panic("map never tracked")
	}
	return im.(*insertionMap[K, V])
}

func (i *insertionMap[K, V]) put(m map[K]V, k K, v V) {
	i.keyIndicesLock.Lock()
	if i.keyIndices == nil {
		i.keyIndices = map[K]int{k: i.keyCounter}
	} else {
		i.keyIndices[k] = len(i.keyIndices) + 1
		if i.keyCounter > reindexKeysAfterCounter && i.keyCounter-len(i.keyIndices) > reindexKeysAfterGap {
			i.reindexKeysUnlocked()
		}
	}
	i.keyCounter++
	i.keyIndicesLock.Unlock()
	// TODO(cretz): Might as well put all ops under lock and make all maps
	// concurrency safe?
	m[k] = v
}

func (i *insertionMap[K, V]) delete(m map[K]V, k K) {
	i.keyIndicesLock.Lock()
	delete(i.keyIndices, k)
	i.keyIndicesLock.Unlock()
	delete(m, k)
}

func (i *insertionMap[K, V]) iter(m map[K]V) *MapIter[K, V] {
	// Get keys in sorted order
	// TODO(cretz): Could make this way more performant but I'm lazy
	i.keyIndicesLock.RLock()
	keys := make([]K, 0, len(i.keyIndices))
	for k := range i.keyIndices {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool { return i.keyIndices[keys[a]] < i.keyIndices[keys[b]] })
	i.keyIndicesLock.RUnlock()
	return &MapIter[K, V]{
		initialKeys: keys,
		m:           m,
		keyIndex:    -1,
	}
}

func (i *insertionMap[K, V]) reindexKeysUnlocked() {
	// Collect keys sorted by index then update indices w/ new index
	// TODO(cretz): Could make this way more performant but I'm lazy
	keys := make([]K, 0, len(i.keyIndices))
	for k := range i.keyIndices {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool { return i.keyIndices[keys[a]] < i.keyIndices[keys[b]] })
	for index, k := range keys {
		i.keyIndices[k] = index
	}
	i.keyCounter = len(i.keyIndices)
}
