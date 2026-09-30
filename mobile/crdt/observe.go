package crdt

import (
	"sync"
)

// UpdateObserver receives one notification per committed transaction:
// the incremental V1 update, and whether it came from this device (a
// local edit, an undo, a redo) or from a peer. Implement it in Kotlin
// and pass it to ObserveUpdates. OnUpdate runs on a background
// goroutine, in commit order, holding no locks; post to the app's own
// dispatcher from it and return promptly, because nothing is delivered
// until it does.
type UpdateObserver interface {
	OnUpdate(update []byte, local bool)
}

// Subscription stops an observer. Close is idempotent and safe from any
// thread. After it returns no further updates are queued; one already
// dequeued may still run to completion.
type Subscription struct {
	once sync.Once
	stop func()
}

// Close stops delivery.
func (s *Subscription) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if s.stop != nil {
			s.stop()
		}
	})
}

// pending is the FIFO feeding one observer. The bridge appends under its
// mutex from inside the committing call; a drain goroutine pops and
// delivers with no locks held, so a callback may re-enter any Doc
// method. Updates are delivered one per transaction, unmerged: the app
// relays each as it happened.
type pending struct {
	mu      sync.Mutex
	cond    *sync.Cond
	queue   [][]byte
	local   []bool
	stopped bool
}

// ObserveUpdates registers obs to receive every update this document
// commits, local or remote, and returns the Subscription that detaches
// it. On a closed document it returns an already-closed Subscription.
func (y *Doc) ObserveUpdates(obs UpdateObserver) *Subscription {
	y.mu.Lock()
	if y.d == nil {
		y.mu.Unlock()
		s := &Subscription{}
		s.once.Do(func() {})
		return s
	}
	p := &pending{}
	p.cond = sync.NewCond(&p.mu)

	// The bridge runs inside the committing call, which holds y.mu. It
	// must be cheap: it drops empty updates, computes local, enqueues
	// under p.mu, and returns. It never takes y.mu and never calls the
	// observer.
	stopBridge := y.d.OnUpdate(func(update []byte, origin any) {
		if isEmptyUpdate(update) {
			return
		}
		p.mu.Lock()
		if p.stopped {
			p.mu.Unlock()
			return
		}
		p.queue = append(p.queue, update)
		p.local = append(p.local, origin != remoteOrigin)
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

	go drain(p, obs)
	return sub
}

// drain delivers queued updates to obs in commit order until the
// subscription stops, abandoning what is still queued at that point.
func drain(p *pending, obs UpdateObserver) {
	for {
		p.mu.Lock()
		for len(p.queue) == 0 && !p.stopped {
			p.cond.Wait()
		}
		if p.stopped {
			p.mu.Unlock()
			return
		}
		upd := p.queue[0]
		local := p.local[0]
		p.queue[0] = nil
		p.queue = p.queue[1:]
		p.local = p.local[1:]
		p.mu.Unlock()
		obs.OnUpdate(upd, local)
	}
}
