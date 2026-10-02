package scanner

import "sync"

// taskQueue is an unbounded work queue for directory tasks with completion
// detection: pop blocks while work is in flight and returns ok=false once
// every pushed task has been processed. A bounded channel cannot do this
// safely with a fixed pool of workers that also produce tasks (all workers
// can block on send with the buffer full and nobody receiving).
type taskQueue struct {
	mu      sync.Mutex
	cond    *sync.Cond
	items   []dirTask
	pending int // queued + in-flight
	closed  bool
}

func newTaskQueue(root dirTask) *taskQueue {
	q := &taskQueue{items: []dirTask{root}, pending: 1}
	q.cond = sync.NewCond(&q.mu)

	return q
}

// push registers a new task. Callers must only push while holding an
// outstanding task (so pending never transiently hits zero).
func (q *taskQueue) push(t dirTask) {
	q.mu.Lock()
	q.items = append(q.items, t)
	q.pending++
	q.mu.Unlock()
	q.cond.Signal()
}

// pop blocks until a task is available. ok=false means the scan is finished.
func (q *taskQueue) pop() (dirTask, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}

	if len(q.items) == 0 {
		return dirTask{}, false
	}

	t := q.items[0]
	q.items[0] = dirTask{} // don't pin path strings
	q.items = q.items[1:]

	return t, true
}

// doneOne marks the caller's task finished; it closes the queue when nothing
// remains queued or in flight.
func (q *taskQueue) doneOne() {
	q.mu.Lock()

	q.pending--
	if q.pending == 0 {
		q.closed = true
		q.cond.Broadcast()
	}
	q.mu.Unlock()
}
