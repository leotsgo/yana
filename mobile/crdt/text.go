package crdt

import (
	"encoding/json"
	"sync"

	ycrdt "github.com/reearth/ygo/crdt"
)

// textHunk is one replacement region of a transaction, in the coordinates
// of the text before it: pos is a UTF-16 offset, del the units the
// transaction removed at pos, and ins the units it put there. A pure
// insert is del 0; a pure delete, ins 0. The JSON field names are the
// wire contract with the editor's Kotlin side.
type textHunk struct {
	Pos int `json:"p"`
	Del int `json:"d"`
	Ins int `json:"i"`
}

// hunks converts a transaction's rich-text delta into replacement hunks.
// Positions are UTF-16 offsets in the text before the transaction. Ops in
// one contiguous run — an insert and a delete with no retain between —
// edit the same region and coalesce into one hunk whatever their order,
// because the delta's op order there (insert-then-delete or the reverse)
// does not change the text it produces.
func hunks(delta []ycrdt.Delta) []textHunk {
	var out []textHunk
	pos := 0    // old-coordinate position of the next op
	region := 0 // start of the run of deletes and inserts in progress
	add := func(del, ins int) {
		if n := len(out); n > 0 && out[n-1].Pos == region {
			out[n-1].Del += del
			out[n-1].Ins += ins
			return
		}
		out = append(out, textHunk{Pos: region, Del: del, Ins: ins})
	}
	for _, op := range delta {
		switch op.Op {
		case ycrdt.DeltaOpRetain:
			pos += op.Retain
			if op.Retain > 0 {
				region = pos
			}
		case ycrdt.DeltaOpDelete:
			add(op.Delete, 0)
			pos += op.Delete
		case ycrdt.DeltaOpInsert:
			s, _ := op.Insert.(string)
			add(0, utf16Len(s))
		}
	}
	return out
}

// TextObserver receives one notification per committed transaction that
// changed the body: the text after it, the change as replacement hunks
// (a JSON array like [{"p":3,"d":0,"i":5}], empty when absent), and
// whether this device authored it (an edit, an undo, a redo) or a peer
// did. Implement it in Kotlin and pass it to ObserveText. OnText runs on
// a background goroutine, in commit order, holding no locks.
type TextObserver interface {
	OnText(text string, delta string, local bool)
}

// textEvent is one queued change on its way to an observer.
type textEvent struct {
	delta string
	local bool
}

// textPending is the FIFO feeding one text observer; the same shape as
// the update feed in observe.go.
type textPending struct {
	mu      sync.Mutex
	cond    *sync.Cond
	queue   []textEvent
	stopped bool
}

// ObserveText registers obs to receive every text change this document
// commits, local or remote, and returns the Subscription that detaches
// it. On a closed document it returns an already-closed Subscription.
func (y *Doc) ObserveText(obs TextObserver) *Subscription {
	y.mu.Lock()
	if y.d == nil {
		y.mu.Unlock()
		s := &Subscription{}
		s.once.Do(func() {})
		return s
	}
	p := &textPending{}
	p.cond = sync.NewCond(&p.mu)

	// The bridge runs inside the committing call. It only derives hunks
	// and enqueues: it must not re-enter the document, whose lock the
	// caller holds. The drain goroutine reads the text with no locks
	// held, so it may.
	stopBridge := y.text.Observe(func(e ycrdt.YTextEvent) {
		hs := hunks(e.Delta)
		if len(hs) == 0 {
			return
		}
		enc, err := json.Marshal(hs)
		if err != nil {
			return
		}
		p.mu.Lock()
		if p.stopped {
			p.mu.Unlock()
			return
		}
		// The port tags an undo or redo transaction with the undo manager
		// as its origin, so — as in observe.go — local means "anything but
		// the remote origin".
		p.queue = append(p.queue, textEvent{delta: string(enc), local: e.Txn.Origin != remoteOrigin})
		p.cond.Signal()
		p.mu.Unlock()
	})
	y.mu.Unlock()

	var sub *Subscription
	sub = &Subscription{stop: func() {
		stopBridge()
		p.mu.Lock()
		p.stopped = true
		p.cond.Broadcast()
		p.mu.Unlock()
		y.subsMu.Lock()
		delete(y.subs, sub)
		y.subsMu.Unlock()
	}}

	y.subsMu.Lock()
	if y.subs == nil {
		y.subs = make(map[*Subscription]struct{})
	}
	y.subs[sub] = struct{}{}
	y.subsMu.Unlock()

	go drainText(y, p, obs)
	return sub
}

// drainText delivers queued changes in commit order, reading the text
// for each outside every lock. It stops when the subscription stops.
func drainText(y *Doc, p *textPending, obs TextObserver) {
	for {
		p.mu.Lock()
		for len(p.queue) == 0 && !p.stopped {
			p.cond.Wait()
		}
		if p.stopped {
			p.mu.Unlock()
			return
		}
		ev := p.queue[0]
		p.queue[0] = textEvent{}
		p.queue = p.queue[1:]
		p.mu.Unlock()
		obs.OnText(y.Text(), ev.delta, ev.local)
	}
}
