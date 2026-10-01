package demo

// The demo world is generated with std::mt19937 and libstdc++'s
// distributions, shuffle and priority_queue. These replicas reproduce the
// exact sequences on Linux so the port builds the same terrain, waypoints and
// paths as the original.

// mt19937 is std::mt19937.
type mt19937 struct {
	state [624]uint32
	index int
}

func newMT19937(seed uint32) *mt19937 {
	m := &mt19937{index: 624}
	m.state[0] = seed
	for i := 1; i < 624; i++ {
		previous := m.state[i-1]
		m.state[i] = 1812433253*(previous^(previous>>30)) + uint32(i)
	}
	return m
}

func (m *mt19937) next() uint32 {
	if m.index >= 624 {
		for i := 0; i < 624; i++ {
			y := m.state[i]&0x80000000 | m.state[(i+1)%624]&0x7fffffff
			value := m.state[(i+397)%624] ^ y>>1
			if y&1 != 0 {
				value ^= 0x9908b0df
			}
			m.state[i] = value
		}
		m.index = 0
	}
	y := m.state[m.index]
	m.index++
	y ^= y >> 11
	y ^= y << 7 & 0x9d2c5680
	y ^= y << 15 & 0xefc60000
	y ^= y >> 18
	return y
}

// generateCanonical is std::generate_canonical<double, 53> over mt19937: two
// draws combined in double precision.
func (m *mt19937) generateCanonical() float64 {
	const r = 4294967296.0
	sum := float64(m.next())
	sum += float64(m.next()) * r
	result := sum / (r * r)
	if result >= 1 {
		result = 0.99999999999999989 // nextafter(1, 0)
	}
	return result
}

// bernoulli is std::bernoulli_distribution(p).
func (m *mt19937) bernoulli(p float64) bool { return m.generateCanonical() < p*1 }

// uniformBelow is std::uniform_int_distribution{0, n-1} over a 32-bit URNG
// (Lemire's nearly-divisionless reduction, as in libstdc++).
func (m *mt19937) uniformBelow(n uint64) uint64 {
	if n-1 >= 0xffffffff {
		// Not reached by the demo (ranges stay far below 2^32).
		panic("uniformBelow: range too large")
	}
	rangeValue := uint32(n)
	product := uint64(m.next()) * uint64(rangeValue)
	low := uint32(product)
	if low < rangeValue {
		threshold := -rangeValue % rangeValue
		for low < threshold {
			product = uint64(m.next()) * uint64(rangeValue)
			low = uint32(product)
		}
	}
	return product >> 32
}

// shuffle is std::shuffle with a 32-bit URNG: pairs of swap positions come
// from one draw while the range allows it.
func shuffle[T any](items []T, m *mt19937) {
	n := uint64(len(items))
	if n == 0 {
		return
	}
	const urngRange = uint64(0xffffffff)
	if urngRange/n >= n {
		i := uint64(1)
		if n%2 == 0 {
			j := m.uniformBelow(2)
			items[i], items[j] = items[j], items[i]
			i++
		}
		for i != n {
			swapRange := i + 1
			x := m.uniformBelow(swapRange * (swapRange + 1))
			first, second := x/(swapRange+1), x%(swapRange+1)
			items[i], items[first] = items[first], items[i]
			i++
			items[i], items[second] = items[second], items[i]
			i++
		}
		return
	}
	for i := uint64(1); i < n; i++ {
		j := m.uniformBelow(i + 1)
		items[i], items[j] = items[j], items[i]
	}
}

// minHeap is std::priority_queue with a "greater" comparator, using
// libstdc++'s push_heap/pop_heap so ties pop in the same order.
type minHeap[T any] struct {
	items   []T
	greater func(a, b T) bool
}

func (h *minHeap[T]) empty() bool { return len(h.items) == 0 }

func (h *minHeap[T]) top() T { return h.items[0] }

func (h *minHeap[T]) pushHeap(hole, top int, value T) {
	parent := (hole - 1) / 2
	for hole > top && h.greater(h.items[parent], value) {
		h.items[hole] = h.items[parent]
		hole = parent
		parent = (hole - 1) / 2
	}
	h.items[hole] = value
}

func (h *minHeap[T]) push(value T) {
	h.items = append(h.items, value)
	h.pushHeap(len(h.items)-1, 0, value)
}

func (h *minHeap[T]) pop() {
	length := len(h.items)
	if length > 1 {
		last := length - 1
		value := h.items[last]
		h.items[last] = h.items[0]
		// __adjust_heap(first, 0, last, value)
		hole, top, child := 0, 0, 0
		for child < (last-1)/2 {
			child = 2 * (child + 1)
			if h.greater(h.items[child], h.items[child-1]) {
				child--
			}
			h.items[hole] = h.items[child]
			hole = child
		}
		if last&1 == 0 && child == (last-2)/2 {
			child = 2 * (child + 1)
			h.items[hole] = h.items[child-1]
			hole = child - 1
		}
		h.pushHeap(hole, top, value)
	}
	h.items = h.items[:length-1]
}
