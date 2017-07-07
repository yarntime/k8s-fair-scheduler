package util

type HeapElement interface {
	score() int
}

type Heap struct {
	max  int
	tree []HeapElement
}

// new Heap with default size: 50
func NewHeap() *Heap {
	return &Heap{0, make([]HeapElement, 50)}
}

func NewSize(size int) *Heap {
	return &Heap{0, make([]HeapElement, size)}
}

func (h *Heap) Empty() bool {
	return h.max == 0
}

func (h *Heap) Len() int {
	return h.max
}

func (h *Heap) Add(e HeapElement) {
	if h.max+1 >= len(h.tree) {
		h.resize()
	}
	h.max++
	h.tree[h.max] = e
	h.up(h.max)
}

func (h *Heap) Remove() HeapElement {

	if h.max == 0 {
		return nil
	}

	min := h.tree[1] // root
	h.tree[1] = h.tree[h.max]
	h.tree[h.max] = nil
	h.max--
	h.down(1)

	return min
}

func (h *Heap) up(pos int) {

	// you are root
	if pos == 1 {
		return
	}

	// less is more (son < father)
	if h.tree[pos].score() < h.tree[pos/2].score() {
		h.swap(pos/2, pos)
		h.up(pos / 2)
	}
}

func (h *Heap) down(father int) {

	son := h.highScoreSon(father*2, (father*2)+1)

	if son != -1 {
		if h.tree[father].score() > h.tree[son].score() {
			h.swap(father, son)
			h.down(son)
		}
	}
}

// index of son with highest pri, or -1 if no children
func (h *Heap) highScoreSon(first, second int) int {

	// they are both larger than last element in heap
	if first > h.max || h.tree[first] == nil {
		return -1
	} else if h.tree[second] == nil {
		return first
	} else if h.tree[first].score() > h.tree[second].score() {
		return second
	}

	return first
}

func (h *Heap) swap(i, j int) {
	h.tree[j], h.tree[i] = h.tree[i], h.tree[j]
}

// double capacity
func (h *Heap) resize() {
	h.tree = append(h.tree, make([]HeapElement, len(h.tree))...)
}
