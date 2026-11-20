package spatial

import (
	"cmp"
	"math"
	"slices"

	"github.com/yohamta/donburi"
	"github.com/yohamta/donburi/filter"
)

// QueryRect returns the entries whose indexed position lies inside rect,
// optionally narrowed by the supplied filters (for example
// filter.Contains(Enemy)). With no filter, every indexed entity matches. When
// several filters are given, all of them must match.
//
// The result is sorted in ascending Entity order (id first, then version), so
// repeated queries against an unchanged world return the same order.
//
// Both rectangle edges are inclusive. An invalid rectangle (non-finite edge or
// MinX > MaxX / MinY > MaxY) yields an empty result.
//
// Queries reconcile the index with the world first unless the index was
// created with WithManualSync.
func (idx *Index[T]) QueryRect(rect Rect, filters ...filter.LayoutFilter) []*donburi.Entry {
	if !rect.Valid() {
		return nil
	}
	return idx.query(rect.MinX, rect.MinY, rect.MaxX, rect.MaxY, rect.Contains, combineFilters(filters))
}

// QueryRadius returns the entries whose indexed position is at distance radius
// or less from (x, y), optionally narrowed by the supplied filters. With no
// filter, every indexed entity matches. When several filters are given, all of
// them must match.
//
// The boundary is inclusive: a point exactly radius away is returned. A zero
// radius returns only entities exactly at (x, y). A negative, NaN, or infinite
// radius, or a non-finite center, yields an empty result.
//
// The result is sorted in ascending Entity order. Queries reconcile the index
// with the world first unless the index was created with WithManualSync.
func (idx *Index[T]) QueryRadius(x, y, radius float64, filters ...filter.LayoutFilter) []*donburi.Entry {
	if !isFinite(x) || !isFinite(y) || !isFinite(radius) || radius < 0 {
		return nil
	}

	// Comparing distances with math.Hypot avoids the overflow that squaring
	// huge coordinates would produce.
	accept := func(px, py float64) bool {
		return math.Hypot(px-x, py-y) <= radius
	}
	return idx.query(x-radius, y-radius, x+radius, y+radius, accept, combineFilters(filters))
}

// QueryCircle is an alias for QueryRadius.
func (idx *Index[T]) QueryCircle(x, y, radius float64, filters ...filter.LayoutFilter) []*donburi.Entry {
	return idx.QueryRadius(x, y, radius, filters...)
}

// combineFilters folds the supplied filters into one, ignoring nil entries. It
// returns nil when nothing is left to check.
func combineFilters(filters []filter.LayoutFilter) filter.LayoutFilter {
	active := make([]filter.LayoutFilter, 0, len(filters))
	for _, f := range filters {
		if f != nil {
			active = append(active, f)
		}
	}
	switch len(active) {
	case 0:
		return nil
	case 1:
		return active[0]
	default:
		return filter.And(active...)
	}
}

func (idx *Index[T]) query(minX, minY, maxX, maxY float64, accept func(x, y float64) bool, f filter.LayoutFilter) []*donburi.Entry {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if !idx.manualSync {
		idx.syncLocked()
	}

	var result []*donburi.Entry

	visit := func(entity donburi.Entity) {
		rec := idx.records[entity]
		if rec == nil || (!rec.inGrid && !rec.inFar) {
			// Non-finite positions are tracked but never queryable.
			return
		}
		if !accept(rec.x, rec.y) {
			return
		}
		// Guard against entries destroyed since the last reconciliation. In
		// automatic mode this is already guaranteed; in manual mode it keeps
		// stale entities (including reused ids) out of the result.
		if !idx.world.Valid(entity) {
			return
		}
		entry := idx.world.Entry(entity)
		// In manual mode a record can outlive the component: refuse entities
		// that no longer carry the indexed position component.
		if !entry.HasComponent(idx.component) {
			return
		}
		if f != nil && !f.MatchesLayout(entry.Archetype().Layout().Components()) {
			return
		}
		result = append(result, entry)
	}

	// Decide whether to walk the cells covered by the query or the cells that
	// are actually occupied. This keeps a query for a huge rectangle bounded
	// by the number of occupied cells instead of the number of requested
	// cells, which can be astronomically large for coordinates near the
	// float64 limit.
	loX, hiX, okX := cellRange(minX, maxX, idx.cellSize)
	loY, hiY, okY := cellRange(minY, maxY, idx.cellSize)
	occupied := len(idx.cells)
	scanAll := !okX || !okY
	if !scanAll {
		width := hiX - loX + 1
		height := hiY - loY + 1
		if width > int64(occupied) || height > int64(occupied) ||
			(occupied > 0 && width > int64(occupied)/height) {
			scanAll = true
		}
	}

	if scanAll {
		for _, bucket := range idx.cells {
			for _, entity := range bucket {
				visit(entity)
			}
		}
	} else {
		for cellX := loX; cellX <= hiX; cellX++ {
			for cellY := loY; cellY <= hiY; cellY++ {
				for _, entity := range idx.cells[cellKey{X: cellX, Y: cellY}] {
					visit(entity)
				}
			}
		}
	}

	// Entities with finite positions outside the representable cell range are
	// kept here and tested individually; this set is normally empty.
	for entity := range idx.far {
		visit(entity)
	}

	// A stable sort makes the result deterministic across queries and builds.
	slices.SortStableFunc(result, func(a, b *donburi.Entry) int {
		return cmp.Compare(a.Entity(), b.Entity())
	})
	return result
}

// cellRange returns the first and last cell index covering [min, max]. ok is
// false when the range cannot be represented (non-finite or beyond
// maxCellCoord); callers then fall back to scanning occupied cells.
func cellRange(min, max, cellSize float64) (lo, hi int64, ok bool) {
	if !isFinite(min) || !isFinite(max) {
		return 0, 0, false
	}
	lo, okLo := cellCoord(min, cellSize)
	hi, okHi := cellCoord(max, cellSize)
	if !okLo || !okHi || lo > hi {
		return 0, 0, false
	}
	return lo, hi, true
}
