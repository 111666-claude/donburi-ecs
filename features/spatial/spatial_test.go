package spatial_test

import (
	"math"
	"sort"
	"testing"

	"github.com/yohamta/donburi"
	"github.com/yohamta/donburi/features/spatial"
	"github.com/yohamta/donburi/filter"
)

type Position struct {
	X, Y float64
}

var (
	position = donburi.NewComponentType[Position]()
	enemy    = donburi.NewTag("Enemy")
	boss     = donburi.NewTag("Boss")
)

func setPosition(t *testing.T, w donburi.World, e donburi.Entity, x, y float64) {
	t.Helper()
	entry := w.Entry(e)
	if !entry.Valid() {
		t.Fatalf("entity %v is not valid", e)
	}
	donburi.SetValue(entry, position, Position{X: x, Y: y})
}

func entityIDs(entries []*donburi.Entry) []donburi.Entity {
	ids := make([]donburi.Entity, len(entries))
	for i, e := range entries {
		ids[i] = e.Entity()
	}
	return ids
}

func sortedIDs(entries []*donburi.Entry) []donburi.Entity {
	ids := entityIDs(entries)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func assertIDs(t *testing.T, got []*donburi.Entry, want []donburi.Entity) {
	t.Helper()
	gotIDs := entityIDs(got)
	if len(gotIDs) != len(want) {
		t.Fatalf("got %d entries %v, want %d %v", len(gotIDs), gotIDs, len(want), want)
	}
	for i := range want {
		if gotIDs[i] != want[i] {
			t.Fatalf("got %v, want %v", gotIDs, want)
		}
	}
}

func TestQueryRectBasic(t *testing.T) {
	w := donburi.NewWorld()
	a := w.Create(position)
	b := w.Create(position)
	c := w.Create(position)
	d := w.Create(position)
	setPosition(t, w, a, 10, 10)
	setPosition(t, w, b, 20, 20)
	setPosition(t, w, c, 90, 90)
	setPosition(t, w, d, -5, -5)

	idx := spatial.NewIndex(w, position)

	got := idx.QueryRect(spatial.NewRect(0, 0, 50, 50), nil)
	assertIDs(t, got, []donburi.Entity{a, b})
}

func TestQueryRectInclusiveBoundary(t *testing.T) {
	w := donburi.NewWorld()
	onEdge := w.Create(position)
	justOutside := w.Create(position)
	setPosition(t, w, onEdge, 10, 5)
	setPosition(t, w, justOutside, 10.0001, 5)

	idx := spatial.NewIndex(w, position)
	got := idx.QueryRect(spatial.Rect{MinX: 0, MinY: 0, MaxX: 10, MaxY: 10}, nil)
	assertIDs(t, got, []donburi.Entity{onEdge})
}

func TestQueryRadiusBoundaryAndZeroRadius(t *testing.T) {
	w := donburi.NewWorld()
	center := w.Create(position)
	tangent := w.Create(position)
	inside := w.Create(position)
	outside := w.Create(position)
	setPosition(t, w, center, 0, 0)
	setPosition(t, w, tangent, 3, 4) // distance exactly 5
	setPosition(t, w, inside, 1, 1)
	setPosition(t, w, outside, 0, 5.0001)

	idx := spatial.NewIndex(w, position)

	assertIDs(t, idx.QueryRadius(0, 0, 5, nil), []donburi.Entity{
		center, tangent, inside,
	})

	// Zero radius returns only the exact point.
	assertIDs(t, idx.QueryRadius(0, 0, 0, nil), []donburi.Entity{center})
	// Negative radius is rejected.
	if got := idx.QueryRadius(0, 0, -1, nil); len(got) != 0 {
		t.Fatalf("negative radius should return nothing, got %v", entityIDs(got))
	}
}

func TestNegativeCoordinates(t *testing.T) {
	w := donburi.NewWorld()
	a := w.Create(position)
	b := w.Create(position)
	c := w.Create(position)
	setPosition(t, w, a, -0.5, -0.5)
	setPosition(t, w, b, -100, -100)
	setPosition(t, w, c, 100, 100)

	idx := spatial.NewIndex(w, position)
	assertIDs(t, idx.QueryRect(spatial.NewRect(-2, -2, -0.1, -0.1), nil), []donburi.Entity{a})
	assertIDs(t, idx.QueryRadius(-100, -100, 0.25, nil), []donburi.Entity{b})
}

func TestQueryReturnsStableSortedOrder(t *testing.T) {
	w := donburi.NewWorld()
	entities := w.CreateMany(200, position)
	// Scatter so that archetype storage order differs from entity order.
	for i, e := range entities {
		setPosition(t, w, e, float64((i*37)%200), float64((i*91)%200))
	}

	idx := spatial.NewIndex(w, position)
	first := entityIDs(idx.QueryRect(spatial.NewRect(-1000, -1000, 1000, 1000), nil))
	if !sort.SliceIsSorted(first, func(i, j int) bool { return first[i] < first[j] }) {
		t.Fatalf("result must be sorted: %v", first)
	}
	for i := 0; i < 5; i++ {
		again := entityIDs(idx.QueryRect(spatial.NewRect(-1000, -1000, 1000, 1000), nil))
		if len(again) != len(first) {
			t.Fatalf("result length changed between queries")
		}
		for j := range first {
			if again[j] != first[j] {
				t.Fatalf("result order changed between queries")
			}
		}
	}
}

func TestFilterIsApplied(t *testing.T) {
	w := donburi.NewWorld()
	e1 := w.Create(position, enemy)
	e2 := w.Create(position, enemy, boss)
	e3 := w.Create(position)
	setPosition(t, w, e1, 1, 1)
	setPosition(t, w, e2, 2, 2)
	setPosition(t, w, e3, 3, 3)

	idx := spatial.NewIndex(w, position)

	assertIDs(t, idx.QueryRect(spatial.NewRect(0, 0, 10, 10), filter.Contains(enemy)),
		[]donburi.Entity{e1, e2})
	assertIDs(t, idx.QueryRect(spatial.NewRect(0, 0, 10, 10), filter.Contains(enemy, boss)),
		[]donburi.Entity{e2})
	assertIDs(t, idx.QueryRadius(0, 0, 10, filter.Not(filter.Contains(enemy))),
		[]donburi.Entity{e3})
}

func TestPositionUpdatesAreReflected(t *testing.T) {
	w := donburi.NewWorld()
	e := w.Create(position)
	setPosition(t, w, e, 0, 0)
	idx := spatial.NewIndex(w, position)

	assertIDs(t, idx.QueryRect(spatial.NewRect(-1, -1, 1, 1), nil), []donburi.Entity{e})
	assertIDs(t, idx.QueryRect(spatial.NewRect(99, 99, 101, 101), nil), nil)

	// Move without notifying the index; the next query must see the change.
	setPosition(t, w, e, 100, 100)
	assertIDs(t, idx.QueryRect(spatial.NewRect(-1, -1, 1, 1), nil), nil)
	assertIDs(t, idx.QueryRect(spatial.NewRect(99, 99, 101, 101), nil), []donburi.Entity{e})
}

func TestMultiFrameMovement(t *testing.T) {
	w := donburi.NewWorld()
	e := w.Create(position)
	idx := spatial.NewIndex(w, position)

	for frame := 0; frame < 20; frame++ {
		x := float64(frame * 10)
		setPosition(t, w, e, x, 0)
		assertIDs(t, idx.QueryRadius(x, 0, 0.5, nil), []donburi.Entity{e})
		if frame > 0 {
			prev := float64((frame - 1) * 10)
			assertIDs(t, idx.QueryRadius(prev, 0, 0.5, nil), nil)
		}
	}
}

func TestDestroyedEntityIsNotReturned(t *testing.T) {
	w := donburi.NewWorld()
	a := w.Create(position)
	b := w.Create(position)
	setPosition(t, w, a, 0, 0)
	setPosition(t, w, b, 1, 1)

	idx := spatial.NewIndex(w, position)
	assertIDs(t, idx.QueryRect(spatial.NewRect(0, 0, 10, 10), nil), []donburi.Entity{a, b})

	w.Remove(a)
	if got := idx.QueryRect(spatial.NewRect(0, 0, 10, 10), nil); len(got) != 1 || got[0].Entity() != b {
		t.Fatalf("destroyed entity must not be returned: %v", entityIDs(got))
	}
	if idx.Len() != 1 {
		t.Fatalf("index should track 1 entity, got %d", idx.Len())
	}
}

func TestReusedEntityIDIsNotStale(t *testing.T) {
	w := donburi.NewWorld()
	old := w.Create(position)
	setPosition(t, w, old, 0, 0)

	idx := spatial.NewIndex(w, position)
	if got := idx.QueryRadius(0, 0, 0, nil); len(got) != 1 || got[0].Entity() != old {
		t.Fatalf("expected original entity, got %v", entityIDs(got))
	}

	w.Remove(old)

	// The world reuses the id; the version differs, so the stale position must
	// not leak into the new entity's results.
	reused := w.Create(position)
	if reused.Id() != old.Id() {
		t.Fatalf("test expected id reuse, got ids %d and %d", reused.Id(), old.Id())
	}
	setPosition(t, w, reused, 500, 500)

	if got := idx.QueryRadius(0, 0, 0, nil); len(got) != 0 {
		t.Fatalf("stale position must not be returned after id reuse: %v", entityIDs(got))
	}
	assertIDs(t, idx.QueryRadius(500, 500, 0, nil), []donburi.Entity{reused})

	// Destroying the reused entity again must not resurrect the old position.
	w.Remove(reused)
	if got := idx.QueryRadius(500, 500, 0, nil); len(got) != 0 {
		t.Fatalf("destroyed reused entity must not be returned: %v", entityIDs(got))
	}
	if got := idx.QueryRadius(0, 0, 0, nil); len(got) != 0 {
		t.Fatalf("old position must not reappear: %v", entityIDs(got))
	}
}

func TestComponentTransferUpdatesIndex(t *testing.T) {
	w := donburi.NewWorld()
	e := w.Create(position, enemy)
	setPosition(t, w, e, 4, 4)
	idx := spatial.NewIndex(w, position)

	// Adding an unrelated component moves the entity to another archetype.
	w.Entry(e).AddComponent(boss)
	assertIDs(t, idx.QueryRadius(4, 4, 0, nil), []donburi.Entity{e})

	// Removing the position component must remove it from results.
	w.Entry(e).RemoveComponent(position)
	if got := idx.QueryRadius(4, 4, 0, nil); len(got) != 0 {
		t.Fatalf("entity without position must not match: %v", entityIDs(got))
	}
	if idx.Len() != 0 {
		t.Fatalf("index should be empty, got %d", idx.Len())
	}

	// Re-adding the position component brings it back at its (reset) position.
	w.Entry(e).AddComponent(position)
	assertIDs(t, idx.QueryRadius(0, 0, 0, nil), []donburi.Entity{e})
}

func TestNonFinitePositionsAreNeverReturned(t *testing.T) {
	w := donburi.NewWorld()
	nan := w.Create(position)
	posInf := w.Create(position)
	negInf := w.Create(position)
	finite := w.Create(position)
	setPosition(t, w, nan, math.NaN(), 0)
	setPosition(t, w, posInf, math.Inf(1), 0)
	setPosition(t, w, negInf, 0, math.Inf(-1))
	setPosition(t, w, finite, 0, 0)

	idx := spatial.NewIndex(w, position)
	huge := spatial.NewRect(-math.MaxFloat64, -math.MaxFloat64, math.MaxFloat64, math.MaxFloat64)
	assertIDs(t, idx.QueryRect(huge, nil), []donburi.Entity{finite})
	assertIDs(t, idx.QueryRadius(0, 0, math.MaxFloat64, nil), []donburi.Entity{finite})

	// A non-finite query is rejected rather than matching everything.
	if got := idx.QueryRadius(math.NaN(), 0, 10, nil); len(got) != 0 {
		t.Fatalf("NaN center should return nothing")
	}
	if got := idx.QueryRadius(0, 0, math.Inf(1), nil); len(got) != 0 {
		t.Fatalf("infinite radius should return nothing")
	}
	if got := idx.QueryRect(spatial.NewRect(0, 0, math.NaN(), 10), nil); len(got) != 0 {
		t.Fatalf("NaN rect should return nothing")
	}
	if got := idx.QueryRect(spatial.NewRect(10, 0, 0, 10), nil); len(got) != 0 {
		t.Fatalf("inverted rect should return nothing")
	}
}

func TestHugeCoordinatesRemainQueryable(t *testing.T) {
	w := donburi.NewWorld()
	far := w.Create(position)
	setPosition(t, w, far, 1e300, -1e300)

	idx := spatial.NewIndex(w, position)
	assertIDs(t, idx.QueryRadius(1e300, -1e300, 0, nil), []donburi.Entity{far})
	assertIDs(t, idx.QueryRadius(-1e300, 1e300, 0, nil), nil)
}

func TestBulkMoveAndHugeRectangle(t *testing.T) {
	w := donburi.NewWorld()
	entities := w.CreateMany(1000, position)
	idx := spatial.NewIndex(w, position)

	// Bulk move every entity by a large offset; a single huge query must find
	// them all without iterating a huge number of empty cells.
	for i, e := range entities {
		setPosition(t, w, e, 1e15+float64(i), -1e15-float64(i))
	}
	huge := spatial.NewRect(-math.MaxFloat64, -math.MaxFloat64, math.MaxFloat64, math.MaxFloat64)
	got := idx.QueryRect(huge, nil)
	if len(got) != len(entities) {
		t.Fatalf("expected %d entities, got %d", len(entities), len(got))
	}
	if !sort.SliceIsSorted(entityIDs(got), func(i, j int) bool { return got[i].Entity() < got[j].Entity() }) {
		t.Fatalf("bulk result must be sorted")
	}

	// A narrow query around one entity finds exactly it.
	target := entities[500]
	assertIDs(t, idx.QueryRadius(1e15+500, -1e15-500, 0.25, nil), []donburi.Entity{target})
}

func TestEmptyWorld(t *testing.T) {
	w := donburi.NewWorld()
	idx := spatial.NewIndex(w, position)

	if got := idx.QueryRect(spatial.NewRect(-1, -1, 1, 1), nil); len(got) != 0 {
		t.Fatalf("empty world should return no results, got %v", entityIDs(got))
	}
	if got := idx.QueryRadius(0, 0, 100, nil); len(got) != 0 {
		t.Fatalf("empty world should return no results")
	}
	if idx.Len() != 0 {
		t.Fatalf("empty index should have length 0, got %d", idx.Len())
	}
}

func TestManualSyncMode(t *testing.T) {
	w := donburi.NewWorld()
	e := w.Create(position)
	setPosition(t, w, e, 0, 0)

	idx := spatial.NewIndex(w, position, spatial.WithManualSync())
	assertIDs(t, idx.QueryRect(spatial.NewRect(-1, -1, 1, 1), nil), []donburi.Entity{e})

	setPosition(t, w, e, 50, 50)
	// In manual mode the query does not reconcile; the stale record still
	// answers from the old cell until Sync is called.
	if got := idx.QueryRadius(0, 0, 1, nil); len(got) != 1 || got[0].Entity() != e {
		t.Fatalf("manual mode should keep answering from the last sync: %v", entityIDs(got))
	}
	idx.Sync()
	assertIDs(t, idx.QueryRect(spatial.NewRect(49, 49, 51, 51), nil), []donburi.Entity{e})
	assertIDs(t, idx.QueryRadius(0, 0, 1, nil), nil)

	w.Remove(e)
	if got := idx.QueryRadius(50, 50, 1, nil); len(got) != 0 {
		t.Fatalf("destroyed entity must not be returned in manual mode: %v", entityIDs(got))
	}

	// In manual mode a stale record must also be rejected once the position
	// component has been removed.
	other := w.Create(position, enemy)
	setPosition(t, w, other, 7, 7)
	idx.Sync()
	beforeRemove := idx.Len()
	w.Entry(other).RemoveComponent(position)
	if got := idx.QueryRadius(7, 7, 1, nil); len(got) != 0 {
		t.Fatalf("entity without position must not be returned in manual mode: %v", entityIDs(got))
	}
	// Len reflects the state as of the last Sync in manual mode.
	if idx.Len() != beforeRemove {
		t.Fatalf("manual-mode Len should not sync automatically")
	}
	idx.Sync()
	if idx.Len() != beforeRemove-1 {
		t.Fatalf("after Sync, Len should have dropped the transferred entity, got %d", idx.Len())
	}
}

func TestCustomCellSize(t *testing.T) {
	w := donburi.NewWorld()
	e := w.Create(position)
	setPosition(t, w, e, 1000, 1000)

	idx := spatial.NewIndex(w, position, spatial.WithCellSize(1))
	if idx.CellSize() != 1 {
		t.Fatalf("cell size should be 1, got %v", idx.CellSize())
	}
	assertIDs(t, idx.QueryRadius(1000, 1000, 0, nil), []donburi.Entity{e})
}

func TestNewIndexPanicsOnInvalidConfiguration(t *testing.T) {
	w := donburi.NewWorld()

	for _, size := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("cell size %v should panic", size)
				}
			}()
			spatial.NewIndex(w, position, spatial.WithCellSize(size))
		}()
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Fatalf("nil component should panic")
			}
		}()
		spatial.NewIndex[Position](w, nil)
	}()

	type NoPosition struct {
		Z float64
	}
	noPosition := donburi.NewComponentType[NoPosition]()
	func() {
		defer func() {
			if recover() == nil {
				t.Fatalf("component without X/Y should panic")
			}
		}()
		spatial.NewIndex(w, noPosition)
	}()
}

func TestEmbeddedPositionFields(t *testing.T) {
	type Embedded struct {
		Position
		Depth float64
	}
	embedded := donburi.NewComponentType[Embedded]()

	w := donburi.NewWorld()
	e := w.Create(embedded)
	donburi.SetValue(w.Entry(e), embedded, Embedded{Position: Position{X: -3, Y: 9}})

	idx := spatial.NewIndex(w, embedded)
	assertIDs(t, idx.QueryRadius(-3, 9, 0, nil), []donburi.Entity{e})
}

func TestNewIndexFunc(t *testing.T) {
	type Transform struct {
		Local Position
	}
	transform := donburi.NewComponentType[Transform]()

	w := donburi.NewWorld()
	e := w.Create(transform)
	donburi.SetValue(w.Entry(e), transform, Transform{Local: Position{X: 7, Y: 8}})

	idx := spatial.NewIndexFunc(w, transform, func(t *Transform) (float64, float64) {
		return t.Local.X, t.Local.Y
	})
	assertIDs(t, idx.QueryRadius(7, 8, 0, nil), []donburi.Entity{e})
}

func TestFloat32PositionFields(t *testing.T) {
	type Position32 struct {
		X, Y float32
	}
	pos32 := donburi.NewComponentType[Position32]()

	w := donburi.NewWorld()
	e := w.Create(pos32)
	donburi.SetValue(w.Entry(e), pos32, Position32{X: 3, Y: 4})

	idx := spatial.NewIndex(w, pos32)
	assertIDs(t, idx.QueryRadius(0, 0, 5, nil), []donburi.Entity{e})
	assertIDs(t, idx.QueryRadius(0, 0, 4.99, nil), nil)
}

// --- Oracle comparisons against the core Query API -------------------------

// randomWorld builds a deterministic pseudo-random world with a mix of
// component layouts and positions.
func randomWorld(t *testing.T, n int) donburi.World {
	t.Helper()
	w := donburi.NewWorld()
	next := uint64(12345)
	rand := func(max int) int {
		next = next*6364136223846793005 + 1442695040888963407
		return int((next >> 33) % uint64(max))
	}
	for i := 0; i < n; i++ {
		var e donburi.Entity
		switch rand(4) {
		case 0:
			e = w.Create(position)
		case 1:
			e = w.Create(position, enemy)
		case 2:
			e = w.Create(position, boss)
		default:
			e = w.Create(position, enemy, boss)
		}
		// Coordinates in [-500, 500), including negatives.
		setPosition(t, w, e, float64(rand(1000)-500)+0.5, float64(rand(1000)-500)+0.5)
	}
	return w
}

// bruteForce returns the entities matching f whose position is accepted,
// scanning the world through the ordinary Query machinery.
func bruteForce(w donburi.World, f filter.LayoutFilter, accept func(Position) bool) []donburi.Entity {
	q := donburi.NewQuery(filter.And(filter.Contains(position), f))
	var out []donburi.Entity
	q.Each(w, func(entry *donburi.Entry) {
		if accept(*position.Get(entry)) {
			out = append(out, entry.Entity())
		}
	})
	return out
}

func sortEntities(entities []donburi.Entity) {
	sort.Slice(entities, func(i, j int) bool { return entities[i] < entities[j] })
}

// filterOrEverything normalises a nil filter (which the spatial index treats as
// "match everything") into a filter that the core query can use.
func filterOrEverything(f filter.LayoutFilter) filter.LayoutFilter {
	if f == nil {
		return filter.Contains()
	}
	return f
}

func TestQueryRectMatchesBruteForce(t *testing.T) {
	w := randomWorld(t, 500)
	idx := spatial.NewIndex(w, position, spatial.WithCellSize(17))

	cases := []spatial.Rect{
		spatial.NewRect(-10, -10, 10, 10),
		spatial.NewRect(0, 0, 0, 0),
		spatial.NewRect(-500, -500, 500, 500),
		spatial.NewRect(-200, 100, -50, 400),
		spatial.NewRect(-1e9, -1e9, 1e9, 1e9),
	}
	filters := []struct {
		name string
		f    filter.LayoutFilter
	}{
		{"all", nil},
		{"enemy", filter.Contains(enemy)},
		{"boss", filter.Contains(boss)},
		{"notEnemy", filter.Not(filter.Contains(enemy))},
	}

	for _, rect := range cases {
		for _, tc := range filters {
			got := sortedIDs(idx.QueryRect(rect, tc.f))
			want := bruteForce(w, filterOrEverything(tc.f), func(p Position) bool {
				return rect.Contains(p.X, p.Y)
			})
			sortEntities(want)
			if len(got) != len(want) {
				t.Fatalf("rect %+v filter %s: got %d, want %d", rect, tc.name, len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("rect %+v filter %s: got %v, want %v", rect, tc.name, got, want)
				}
			}
		}
	}
}

func TestQueryRadiusMatchesBruteForce(t *testing.T) {
	w := randomWorld(t, 500)
	idx := spatial.NewIndex(w, position, spatial.WithCellSize(17))

	type circle struct{ x, y, r float64 }
	circles := []circle{
		{0, 0, 50},
		{-250, 250, 0},
		{-250, 250, 30},
		{0, 0, 1000},
		{-500, -500, 10},
	}
	for _, c := range circles {
		for _, f := range []filter.LayoutFilter{nil, filter.Contains(enemy), filter.Contains(boss)} {
			got := sortedIDs(idx.QueryRadius(c.x, c.y, c.r, f))
			want := bruteForce(w, filterOrEverything(f), func(p Position) bool {
				return math.Hypot(p.X-c.x, p.Y-c.y) <= c.r
			})
			sortEntities(want)
			if len(got) != len(want) {
				t.Fatalf("circle %+v: got %d, want %d", c, len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("circle %+v: got %v, want %v", c, got, want)
				}
			}
		}
	}
}

func TestIncrementalMutationMatchesBruteForce(t *testing.T) {
	w := randomWorld(t, 300)
	idx := spatial.NewIndex(w, position, spatial.WithCellSize(23))

	// Repeatedly move, create, destroy, transfer and re-query, validating
	// against the brute-force oracle every round.
	for round := 0; round < 30; round++ {
		next := uint64(round)
		rand := func(max int) int {
			next = next*6364136223846793005 + 1442695040888963407
			return int((next >> 33) % uint64(max))
		}

		entities := w.Archetypes()
		_ = entities

		// Move every existing entity occasionally.
		q := donburi.NewQuery(filter.Contains(position))
		var all []donburi.Entity
		q.Each(w, func(entry *donburi.Entry) { all = append(all, entry.Entity()) })
		for _, e := range all {
			if rand(2) == 0 {
				p := position.Get(w.Entry(e))
				p.X += float64(rand(21) - 10)
				p.Y += float64(rand(21) - 10)
			}
		}

		// Create a few, destroy a few, transfer a few.
		for i := 0; i < 5; i++ {
			e := w.Create(position, enemy)
			setPosition(t, w, e, float64(rand(1000)-500), float64(rand(1000)-500))
			all = append(all, e)
		}
		for i := 0; i < 5 && len(all) > 0; i++ {
			idxToRemove := rand(len(all))
			w.Remove(all[idxToRemove])
			all = append(all[:idxToRemove], all[idxToRemove+1:]...)
		}
		for i := 0; i < 5 && len(all) > 0; i++ {
			e := all[rand(len(all))]
			entry := w.Entry(e)
			if !entry.Valid() {
				continue
			}
			if entry.HasComponent(boss) {
				entry.RemoveComponent(boss)
			} else {
				entry.AddComponent(boss)
			}
		}

		rect := spatial.NewRect(-300, -300, 300, 300)
		got := sortedIDs(idx.QueryRect(rect, filter.Contains(enemy)))
		want := bruteForce(w, filter.Contains(enemy), func(p Position) bool {
			return rect.Contains(p.X, p.Y)
		})
		sortEntities(want)
		if len(got) != len(want) {
			t.Fatalf("round %d: got %d, want %d", round, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("round %d: got %v, want %v", round, got, want)
			}
		}
	}
}

// --- Benchmarks ------------------------------------------------------------

func benchmarkWorld(b *testing.B, n int, opts ...spatial.Option) (donburi.World, *spatial.Index[Position]) {
	b.Helper()
	w := donburi.NewWorld()
	entities := w.CreateMany(n, position)
	for i, e := range entities {
		// Spread entities over a 10000x10000 area.
		donburi.SetValue(w.Entry(e), position, Position{
			X: float64(i%100) * 100,
			Y: float64(i/100) * 100,
		})
	}
	return w, spatial.NewIndex(w, position, opts...)
}

const benchmarkEntityCount = 10000

func BenchmarkQueryRadius(b *testing.B) {
	w, idx := benchmarkWorld(b, benchmarkEntityCount)
	manualWorld, manualIdx := benchmarkWorld(b, benchmarkEntityCount, spatial.WithManualSync())
	manualIdx.Sync()
	_, _ = w, manualWorld

	b.Run("AutoSync", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if got := idx.QueryRadius(5000, 5000, 100, nil); len(got) == 0 {
				b.Fatal("expected matches")
			}
		}
	})

	b.Run("ManualSync", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if got := manualIdx.QueryRadius(5000, 5000, 100, nil); len(got) == 0 {
				b.Fatal("expected matches")
			}
		}
	})

	b.Run("FullScan", func(b *testing.B) {
		plain := donburi.NewQuery(filter.Contains(position))
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			count := 0
			plain.Each(w, func(entry *donburi.Entry) {
				p := position.Get(entry)
				if math.Hypot(p.X-5000, p.Y-5000) <= 100 {
					count++
				}
			})
			if count == 0 {
				b.Fatal("expected matches")
			}
		}
	})
}
