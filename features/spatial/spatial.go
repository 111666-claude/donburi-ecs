// Package spatial provides a dynamic two-dimensional spatial index for Donburi
// worlds.
//
// An Index tracks the entities that have a given position component and maps
// their positions to cells of a uniform grid. It answers rectangle and radius
// (circle) queries without scanning every entity, and the results can be
// narrowed with the same filter.LayoutFilter values used by donburi.Query.
//
// The index stays in sync with the world automatically: every query reads the
// current component data before answering, so creating, destroying, moving
// entities or moving them between archetypes is reflected immediately. For
// workloads that issue many queries per frame, call Sync once per frame and
// construct the index with WithManualSync to skip the per-query reconciliation.
//
// Because Donburi stores component data behind raw pointers, there is no way to
// observe an in-place position write. Detecting such writes therefore requires
// re-reading every indexed position. In the default mode that scan happens once
// per query, so a query costs O(number of indexed entities). With WithManualSync
// and one Sync per frame, a query costs O(visited cells + matches) and is
// independent of the total entity count.
//
// The index is not safe for concurrent use with world mutations. As with the
// rest of Donburi, the world must be treated as single-threaded.
//
// # Resource bounds
//
// An Index stores at most one record and one grid entry per indexed entity, so
// its memory is O(number of entities) regardless of how far apart they are:
// empty cells are deleted as soon as they become empty. Queries never iterate
// the cells of an empty region larger than the number of occupied cells; a
// rectangle or radius spanning most of the float64 range therefore still costs
// O(occupied cells) rather than O(requested area). Positions that are finite
// but too large to map to a cell are kept in a small per-entity overflow set
// and tested exactly, so they remain queryable.
package spatial

import (
	"fmt"
	"math"
	"reflect"
	"sync"
	"unsafe"

	"github.com/yohamta/donburi"
)

// DefaultCellSize is the edge length of the uniform-grid cells used when
// WithCellSize is not supplied.
const DefaultCellSize = 64.0

// maxCellCoord bounds the integer cell coordinates so that grid arithmetic
// cannot overflow. Positions that map outside this range are kept in a small
// overflow set and checked individually at query time; they are never lost.
const maxCellCoord = int64(1) << 40

// Rect is an axis-aligned rectangle in world space.
//
// Both edges are inclusive: a point exactly on an edge is considered to be
// inside the rectangle. A zero-area rectangle (MinX == MaxX and/or
// MinY == MaxY) is valid and matches points on that line or point.
type Rect struct {
	MinX, MinY, MaxX, MaxY float64
}

// NewRect creates a rectangle from its minimum and maximum edges.
func NewRect(minX, minY, maxX, maxY float64) Rect {
	return Rect{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY}
}

// RectFromCenter creates a rectangle centered at (centerX, centerY) with the
// given full width and height.
func RectFromCenter(centerX, centerY, width, height float64) Rect {
	halfWidth := width / 2
	halfHeight := height / 2
	return Rect{
		MinX: centerX - halfWidth,
		MinY: centerY - halfHeight,
		MaxX: centerX + halfWidth,
		MaxY: centerY + halfHeight,
	}
}

// Valid reports whether the rectangle has finite edges and MinX <= MaxX and
// MinY <= MaxY. Queries with an invalid rectangle return no entities.
func (r Rect) Valid() bool {
	return isFinite(r.MinX) && isFinite(r.MinY) &&
		isFinite(r.MaxX) && isFinite(r.MaxY) &&
		r.MinX <= r.MaxX && r.MinY <= r.MaxY
}

// Contains reports whether the point (x, y) is inside the rectangle. The
// boundary is inclusive. Points with NaN coordinates are never contained.
func (r Rect) Contains(x, y float64) bool {
	return x >= r.MinX && x <= r.MaxX && y >= r.MinY && y <= r.MaxY
}

// PositionFunc extracts a two-dimensional position from a component value.
type PositionFunc[T any] func(component *T) (x, y float64)

type config struct {
	cellSize   float64
	manualSync bool
}

// Option configures an Index.
type Option func(*config)

// WithCellSize sets the edge length of the uniform-grid cells. Smaller cells
// make queries more selective at the cost of memory and maintenance time. The
// size must be finite and greater than zero; otherwise NewIndex panics.
func WithCellSize(size float64) Option {
	return func(c *config) {
		c.cellSize = size
	}
}

// WithManualSync disables the automatic reconciliation performed by every
// query. In manual mode the caller must call Sync after mutating the world and
// before querying; this allows one reconciliation per frame instead of one per
// query.
func WithManualSync() Option {
	return func(c *config) {
		c.manualSync = true
	}
}

type cellKey struct {
	X, Y int64
}

type record struct {
	x, y   float64
	cell   cellKey
	bucket int
	inGrid bool
	inFar  bool
}

// Index is a dynamic two-dimensional spatial index over the entities of a
// World that have a particular position component.
//
// The zero value is not usable; create an Index with NewIndex or NewIndexFunc.
type Index[T any] struct {
	mu         sync.Mutex
	world      donburi.World
	component  *donburi.ComponentType[T]
	position   PositionFunc[T]
	cellSize   float64
	manualSync bool
	records    map[donburi.Entity]*record
	cells      map[cellKey][]donburi.Entity
	far        map[donburi.Entity]struct{}
	seen       map[donburi.Entity]struct{}
}

// NewIndex creates a spatial index for the entities of w that have positions.
//
// The position is read from the exported float64 (or float32) fields X and Y
// of the component type. If the component uses a different representation,
// use NewIndexFunc with an explicit accessor instead.
//
// The index is populated immediately and then reconciled automatically before
// every query, unless WithManualSync is supplied.
func NewIndex[T any](w donburi.World, positions *donburi.ComponentType[T], opts ...Option) *Index[T] {
	if positions == nil {
		panic("spatial: nil position component")
	}
	return NewIndexFunc(w, positions, newXYAccessor[T](), opts...)
}

// NewIndexFunc creates a spatial index that reads positions with the supplied
// accessor. This is the constructor to use when the position component does not
// expose X and Y fields.
func NewIndexFunc[T any](w donburi.World, positions *donburi.ComponentType[T], position PositionFunc[T], opts ...Option) *Index[T] {
	if w == nil {
		panic("spatial: nil world")
	}
	if positions == nil {
		panic("spatial: nil position component")
	}
	if position == nil {
		panic("spatial: nil position accessor")
	}

	cfg := config{cellSize: DefaultCellSize}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if !isFinite(cfg.cellSize) || cfg.cellSize <= 0 {
		panic(fmt.Sprintf("spatial: cell size must be finite and positive, got %v", cfg.cellSize))
	}

	idx := &Index[T]{
		world:      w,
		component:  positions,
		position:   position,
		cellSize:   cfg.cellSize,
		manualSync: cfg.manualSync,
		records:    make(map[donburi.Entity]*record),
		cells:      make(map[cellKey][]donburi.Entity),
		far:        make(map[donburi.Entity]struct{}),
		seen:       make(map[donburi.Entity]struct{}),
	}
	idx.Sync()
	return idx
}

// CellSize returns the edge length of the grid cells.
func (idx *Index[T]) CellSize() float64 {
	return idx.cellSize
}

// Len returns the number of entities currently tracked by the index. Entities
// whose position is not finite are tracked but are never returned by queries.
// Like the query methods, Len reconciles the index first unless it was created
// with WithManualSync, in which case it reports the state as of the last Sync.
func (idx *Index[T]) Len() int {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if !idx.manualSync {
		idx.syncLocked()
	}
	return len(idx.records)
}

// Sync reconciles the index with the current state of the world. It adds
// entities that have been created, removes entities that have been destroyed or
// that no longer have the position component, and moves entities whose position
// changed or whose archetype changed.
//
// Queries call Sync automatically unless the index was created with
// WithManualSync.
func (idx *Index[T]) Sync() {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.syncLocked()
}

func (idx *Index[T]) syncLocked() {
	seen := idx.seen
	clear(seen)

	for _, archetype := range idx.world.Archetypes() {
		if !archetype.Layout().HasComponent(idx.component) {
			continue
		}
		for _, entity := range archetype.Entities() {
			entry := idx.world.Entry(entity)
			x, y := idx.position(idx.component.Get(entry))
			seen[entity] = struct{}{}
			idx.track(entity, x, y)
		}
	}

	for entity := range idx.records {
		if _, ok := seen[entity]; !ok {
			idx.untrack(entity)
		}
	}
}

// track inserts or updates the position of an entity.
func (idx *Index[T]) track(entity donburi.Entity, x, y float64) {
	rec, ok := idx.records[entity]
	if ok {
		if math.Float64bits(rec.x) == math.Float64bits(x) &&
			math.Float64bits(rec.y) == math.Float64bits(y) {
			return
		}
		idx.detach(entity, rec)
	} else {
		rec = &record{}
		idx.records[entity] = rec
	}
	rec.x, rec.y = x, y

	// Non-finite positions cannot be compared meaningfully, so they are tracked
	// for lifetime bookkeeping but never placed in a cell.
	if !isFinite(x) || !isFinite(y) {
		return
	}

	cx, okX := cellCoord(x, idx.cellSize)
	cy, okY := cellCoord(y, idx.cellSize)
	if !okX || !okY {
		// Finite, but outside the representable cell range. Keep it in the
		// overflow set so the exact position test can still match it.
		rec.inFar = true
		idx.far[entity] = struct{}{}
		return
	}

	rec.cell = cellKey{X: cx, Y: cy}
	rec.inGrid = true
	bucket := idx.cells[rec.cell]
	rec.bucket = len(bucket)
	idx.cells[rec.cell] = append(bucket, entity)
}

// detach removes an entity from the grid or the overflow set while keeping its
// record.
func (idx *Index[T]) detach(entity donburi.Entity, rec *record) {
	if rec.inGrid {
		bucket := idx.cells[rec.cell]
		last := len(bucket) - 1
		if rec.bucket != last {
			moved := bucket[last]
			bucket[rec.bucket] = moved
			idx.records[moved].bucket = rec.bucket
		}
		bucket = bucket[:last]
		if len(bucket) == 0 {
			delete(idx.cells, rec.cell)
		} else {
			idx.cells[rec.cell] = bucket
		}
		rec.inGrid = false
		rec.bucket = 0
	}
	if rec.inFar {
		delete(idx.far, entity)
		rec.inFar = false
	}
}

func (idx *Index[T]) untrack(entity donburi.Entity) {
	rec := idx.records[entity]
	idx.detach(entity, rec)
	delete(idx.records, entity)
}

func cellCoord(value, cellSize float64) (int64, bool) {
	cell := math.Floor(value / cellSize)
	if math.IsNaN(cell) || math.IsInf(cell, 0) || cell < float64(-maxCellCoord) || cell > float64(maxCellCoord) {
		return 0, false
	}
	return int64(cell), true
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

// newXYAccessor builds a fast accessor for struct components with exported X
// and Y float fields.
func newXYAccessor[T any]() PositionFunc[T] {
	typ := reflect.TypeOf((*T)(nil)).Elem()
	if typ.Kind() != reflect.Struct {
		panic(fmt.Sprintf("spatial: component type %s is not a struct; use NewIndexFunc with a position accessor", typ))
	}
	readX := floatFieldReader(typ, "X")
	readY := floatFieldReader(typ, "Y")
	return func(component *T) (float64, float64) {
		p := unsafe.Pointer(component)
		return readX(p), readY(p)
	}
}

func floatFieldReader(typ reflect.Type, name string) func(unsafe.Pointer) float64 {
	field, ok := typ.FieldByName(name)
	if !ok {
		panic(fmt.Sprintf("spatial: component type %s has no %q field; use NewIndexFunc with a position accessor", typ, name))
	}
	if field.PkgPath != "" {
		panic(fmt.Sprintf("spatial: field %q of component type %s is not exported", name, typ))
	}

	// Resolve the accumulated offset so that embedded structs work too. An
	// embedded pointer cannot be followed with a static offset, so it is
	// rejected in favour of an explicit accessor.
	offset := uintptr(0)
	current := typ
	for _, index := range field.Index {
		f := current.Field(index)
		if f.Anonymous && f.Type.Kind() == reflect.Pointer {
			panic(fmt.Sprintf("spatial: field %q of component type %s is promoted through an embedded pointer; use NewIndexFunc with a position accessor", name, typ))
		}
		offset += f.Offset
		current = f.Type
	}

	switch current.Kind() {
	case reflect.Float64:
		return func(p unsafe.Pointer) float64 {
			return *(*float64)(unsafe.Add(p, offset))
		}
	case reflect.Float32:
		return func(p unsafe.Pointer) float64 {
			return float64(*(*float32)(unsafe.Add(p, offset)))
		}
	default:
		panic(fmt.Sprintf("spatial: field %q of component type %s must be float64 or float32, got %s", name, typ, current.Kind()))
	}
}
