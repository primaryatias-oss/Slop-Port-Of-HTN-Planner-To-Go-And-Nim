## The demo world is generated with std::mt19937 and libstdc++'s
## distributions, shuffle and priority_queue. These replicas reproduce the
## exact sequences on Linux so the port builds the same terrain, waypoints and
## paths as the original.

type
  Mt19937* = object
    ## std::mt19937.
    state: array[624, uint32]
    index: int

  MinHeap*[T] = object
    ## std::priority_queue with a "greater" comparator, using libstdc++'s
    ## push_heap/pop_heap so ties pop in the same order.
    items: seq[T]
    greater: proc (a, b: T): bool {.nimcall.}

proc initMt19937*(seed: uint32): Mt19937 =
  result.index = 624
  result.state[0] = seed
  for i in 1 ..< 624:
    let previous = result.state[i - 1]
    result.state[i] = 1812433253'u32 * (previous xor (previous shr 30)) + uint32(i)

proc next*(m: var Mt19937): uint32 =
  if m.index >= 624:
    for i in 0 ..< 624:
      let y = (m.state[i] and 0x80000000'u32) or (m.state[(i + 1) mod 624] and 0x7fffffff'u32)
      var value = m.state[(i + 397) mod 624] xor (y shr 1)
      if (y and 1) != 0: value = value xor 0x9908b0df'u32
      m.state[i] = value
    m.index = 0
  var y = m.state[m.index]
  inc m.index
  y = y xor (y shr 11)
  y = y xor ((y shl 7) and 0x9d2c5680'u32)
  y = y xor ((y shl 15) and 0xefc60000'u32)
  y xor (y shr 18)

proc generateCanonical*(m: var Mt19937): float64 =
  ## std::generate_canonical<double, 53>: two draws combined in double
  ## precision.
  const r = 4294967296.0
  var sum = float64(m.next())
  sum += float64(m.next()) * r
  result = sum / (r * r)
  if result >= 1.0: result = 0.99999999999999989  # nextafter(1, 0)

proc bernoulli*(m: var Mt19937, p: float64): bool =
  ## std::bernoulli_distribution(p).
  m.generateCanonical() < p * 1.0

proc uniformBelow*(m: var Mt19937, n: uint64): uint64 =
  ## std::uniform_int_distribution{0, n-1} over a 32-bit URNG (Lemire's
  ## nearly-divisionless reduction, as in libstdc++).
  doAssert n - 1 < 0xffffffff'u64, "uniformBelow: range too large"
  let rangeValue = uint32(n)
  var product = uint64(m.next()) * uint64(rangeValue)
  var low = uint32(product and 0xffffffff'u64)
  if low < rangeValue:
    let threshold = (0'u32 - rangeValue) mod rangeValue
    while low < threshold:
      product = uint64(m.next()) * uint64(rangeValue)
      low = uint32(product and 0xffffffff'u64)
  product shr 32

proc shuffle*[T](items: var seq[T], m: var Mt19937) =
  ## std::shuffle with a 32-bit URNG: pairs of swap positions come from one
  ## draw while the range allows it.
  let n = uint64(items.len)
  if n == 0: return
  const urngRange = 0xffffffff'u64
  if urngRange div n >= n:
    var i = 1'u64
    if n mod 2 == 0:
      let j = m.uniformBelow(2)
      swap(items[int(i)], items[int(j)])
      inc i
    while i != n:
      let swapRange = i + 1
      let x = m.uniformBelow(swapRange * (swapRange + 1))
      let first = x div (swapRange + 1)
      let second = x mod (swapRange + 1)
      swap(items[int(i)], items[int(first)])
      inc i
      swap(items[int(i)], items[int(second)])
      inc i
    return
  for i in 1'u64 ..< n:
    let j = m.uniformBelow(i + 1)
    swap(items[int(i)], items[int(j)])

proc initMinHeap*[T](greater: proc (a, b: T): bool {.nimcall.}): MinHeap[T] =
  MinHeap[T](greater: greater)

proc empty*[T](h: MinHeap[T]): bool = h.items.len == 0

proc top*[T](h: MinHeap[T]): T = h.items[0]

proc pushHeap[T](h: var MinHeap[T], hole, top: int, value: T) =
  var hole = hole
  var parent = (hole - 1) div 2
  while hole > top and h.greater(h.items[parent], value):
    h.items[hole] = h.items[parent]
    hole = parent
    parent = (hole - 1) div 2
  h.items[hole] = value

proc push*[T](h: var MinHeap[T], value: T) =
  h.items.add value
  h.pushHeap(h.items.len - 1, 0, value)

proc pop*[T](h: var MinHeap[T]) =
  let length = h.items.len
  if length > 1:
    let last = length - 1
    let value = h.items[last]
    h.items[last] = h.items[0]
    # __adjust_heap(first, 0, last, value)
    var hole = 0
    var child = 0
    while child < (last - 1) div 2:
      child = 2 * (child + 1)
      if h.greater(h.items[child], h.items[child - 1]): dec child
      h.items[hole] = h.items[child]
      hole = child
    if (last and 1) == 0 and child == (last - 2) div 2:
      child = 2 * (child + 1)
      h.items[hole] = h.items[child - 1]
      hole = child - 1
    h.pushHeap(hole, 0, value)
  h.items.setLen(length - 1)
