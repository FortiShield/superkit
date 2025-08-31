package event

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"log/slog"
)

// HandlerFunc is the function being called when receiving an event.
type HandlerFunc func(context.Context, any)

// Emit an event to the given topic
func Emit(topic string, event any) {
	if stream == nil {
		// defensive: should not happen because init() creates the stream
		slog.Warn("event stream not initialized; dropping event", "topic", topic)
		return
	}
	stream.emit(topic, event)
}

// Subscribe a HandlerFunc to the given topic.
// A Subscription is returned that can be used to unsubscribe from the topic.
func Subscribe(topic string, h HandlerFunc) Subscription {
	return stream.subscribe(topic, h)
}

// Unsubscribe unsubscribes the given Subscription from its topic.
func Unsubscribe(sub Subscription) {
	stream.unsubscribe(sub)
}

// Stop stops the event stream, waiting for in-flight handlers to complete.
func Stop() {
	if stream != nil {
		stream.stop()
	}
}

var stream *eventStream

type event struct {
	topic   string
	message any
}

// Subscription represents a handler subscribed to a specific topic.
type Subscription struct {
	ID        uint64
	Topic     string
	CreatedAt int64
	Fn        HandlerFunc
}

type eventStream struct {
	mu      sync.RWMutex
	subs    map[string][]Subscription
	eventch chan event

	// context to cancel running handlers on stop
	ctx    context.Context
	cancel context.CancelFunc

	// wait group to wait for handler goroutines spawned by the stream
	wg sync.WaitGroup

	// ensure stop is only performed once
	stopOnce sync.Once

	// indicator the stream has been stopped
	closed atomic.Bool
}

// global counter for subscription IDs
var subIDCounter atomic.Uint64

func newStream() *eventStream {
	ctx, cancel := context.WithCancel(context.Background())
	e := &eventStream{
		subs:    make(map[string][]Subscription),
		eventch: make(chan event, 128),
		ctx:     ctx,
		cancel:  cancel,
	}
	go e.start()
	return e
}

func (e *eventStream) start() {
	for {
		select {
		case <-e.ctx.Done():
			// context cancelled -> shutdown
			return
		case evt, ok := <-e.eventch:
			if !ok {
				return
			}

			// copy slice of handlers under read lock so we can iterate safely
			e.mu.RLock()
			handlers := append([]Subscription(nil), e.subs[evt.topic]...)
			e.mu.RUnlock()

			if len(handlers) == 0 {
				continue
			}

			for _, sub := range handlers {
				// run each handler in its own goroutine but track with WaitGroup
				e.wg.Add(1)
				go func(s Subscription, msg any) {
					defer e.wg.Done()
					// pass the stream context so handlers can observe cancellation
					s.Fn(e.ctx, msg)
				}(sub, evt.message)
			}
		}
	}
}

func (e *eventStream) stop() {
	e.stopOnce.Do(func() {
		// mark closed so emits can be dropped
		e.closed.Store(true)

		// cancel context to notify handlers
		e.cancel()

		// close event channel to stop the start loop
		// it's safe to close here because stopOnce ensures this runs once
		close(e.eventch)

		// wait for in-flight handlers to finish
		e.wg.Wait()

		// clear subscriptions
		e.mu.Lock()
		e.subs = make(map[string][]Subscription)
		e.mu.Unlock()
	})
}

func (e *eventStream) emit(topic string, v any) {
	// if the stream has been stopped, drop events
	if e.closed.Load() {
		slog.Debug("dropping event because stream is closed", "topic", topic)
		return
	}

	evt := event{
		topic:   topic,
		message: v,
	}

	// Try to send without blocking; if the buffer is full, drop and log.
	select {
	case e.eventch <- evt:
	default:
		// channel full - avoid blocking producers
		slog.Warn("event channel full; dropping event", "topic", topic)
	}
}

func (e *eventStream) subscribe(topic string, h HandlerFunc) Subscription {
	e.mu.Lock()
	defer e.mu.Unlock()

	sub := Subscription{
		ID:        subIDCounter.Add(1),
		CreatedAt: time.Now().UnixNano(),
		Topic:     topic,
		Fn:        h,
	}

	e.subs[topic] = append(e.subs[topic], sub)
	return sub
}

func (e *eventStream) unsubscribe(sub Subscription) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, ok := e.subs[sub.Topic]; ok {
		e.subs[sub.Topic] = slices.DeleteFunc(e.subs[sub.Topic], func(s Subscription) bool {
			return s.ID == sub.ID
		})
		if len(e.subs[sub.Topic]) == 0 {
			delete(e.subs, sub.Topic)
		}
	}
}

func init() {
	stream = newStream()
}
