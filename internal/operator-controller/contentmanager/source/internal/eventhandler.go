package source

// NOTE: To reduce the amount of custom code that needs to be written to facilitate
// the creation of a custom source implementation, the code in this file is copied from
// https://github.com/kubernetes-sigs/controller-runtime/blob/release-0.18/pkg/internal/source/event_handler.go
//
// Any modifications to this code will be noted below:
//   - The "k8s.io/client-go/tools/cache" package was aliased to "cgocache".
//     All references to this package were updated to use the new alias.
//   - The "logf" aliased import was updated from "sigs.k8s.io/controller-runtime/pkg/internal/log"
//     to "sigs.k8s.io/controller-runtime/pkg/log"
//   - The logger for the event handler is now generated with "logf.Log.WithName("source").WithName("EventHandler")"
//     instead of "logf.RuntimeLog.WithName("source").WithName("EventHandler")"
//
// All modifications are licensed under the Apache 2.0 license.
//
// In the future, we may consider making a contribution to the
// controller-runtime project that exports this more generic
// event handler functionality. If that happens, this code will be removed
// and we will instead import the appropriate logic from the controller-runtime
// project.

/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

import (
	"context"
	"errors"
	"fmt"

	cgocache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

var log = logf.Log.WithName("source").WithName("EventHandler")

// NewEventHandler creates a new EventHandler.
func NewEventHandler[object client.Object, request comparable](
	ctx context.Context,
	queue workqueue.TypedRateLimitingInterface[request],
	handler handler.TypedEventHandler[object, request],
	predicates []predicate.TypedPredicate[object]) *EventHandler[object, request] {
	return &EventHandler[object, request]{
		ctx:        ctx,
		handler:    handler,
		queue:      queue,
		predicates: predicates,
	}
}

// EventHandler adapts a handler.EventHandler interface to a cache.ResourceEventHandler interface.
type EventHandler[object client.Object, request comparable] struct {
	// ctx stores the context that created the event handler
	// that is used to propagate cancellation signals to each handler function.
	ctx context.Context

	handler    handler.TypedEventHandler[object, request]
	queue      workqueue.TypedRateLimitingInterface[request]
	predicates []predicate.TypedPredicate[object]
}

// HandlerFuncs converts EventHandler to a ResourceEventHandlerFuncs
// TODO: switch to ResourceEventHandlerDetailedFuncs with client-go 1.27
func (e *EventHandler[object, request]) HandlerFuncs() cgocache.ResourceEventHandlerFuncs {
	return cgocache.ResourceEventHandlerFuncs{
		AddFunc:    e.OnAdd,
		UpdateFunc: e.OnUpdate,
		DeleteFunc: e.OnDelete,
	}
}

// OnAdd creates CreateEvent and calls Create on EventHandler.
func (e *EventHandler[object, request]) OnAdd(obj interface{}) {
	c := event.TypedCreateEvent[object]{}

	// Pull Object out of the object
	if o, ok := obj.(object); ok {
		c.Object = o
	} else {
		log.Error(errors.New("failed to cast object"),
			"OnAdd missing Object",
			"expected_type", fmt.Sprintf("%T", c.Object),
			"received_type", fmt.Sprintf("%T", obj),
			"object", obj)
		return
	}

	for _, p := range e.predicates {
		if !p.Create(c) {
			return
		}
	}

	// Invoke create handler
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	e.handler.Create(ctx, c, e.queue)
}

// OnUpdate creates UpdateEvent and calls Update on EventHandler.
func (e *EventHandler[object, request]) OnUpdate(oldObj, newObj interface{}) {
	u := event.TypedUpdateEvent[object]{}

	if o, ok := oldObj.(object); ok {
		u.ObjectOld = o
	} else {
		log.Error(errors.New("failed to cast old object"),
			"OnUpdate missing ObjectOld",
			"object", oldObj,
			"expected_type", fmt.Sprintf("%T", u.ObjectOld),
			"received_type", fmt.Sprintf("%T", oldObj))
		return
	}

	// Pull Object out of the object
	if o, ok := newObj.(object); ok {
		u.ObjectNew = o
	} else {
		log.Error(errors.New("failed to cast new object"),
			"OnUpdate missing ObjectNew",
			"object", newObj,
			"expected_type", fmt.Sprintf("%T", u.ObjectNew),
			"received_type", fmt.Sprintf("%T", newObj))
		return
	}

	// Run predicates before proceeding
	for _, p := range e.predicates {
		if !p.Update(u) {
			return
		}
	}

	// Invoke update handler
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	e.handler.Update(ctx, u, e.queue)
}

// OnDelete creates DeleteEvent and calls Delete on EventHandler.
func (e *EventHandler[object, request]) OnDelete(obj interface{}) {
	d := event.TypedDeleteEvent[object]{}

	// Handle tombstone events (cache.DeletedFinalStateUnknown)
	if obj == nil {
		log.Error(errors.New("received nil object"),
			"OnDelete received a nil object, ignoring event")
		return
	}

	// Deal with tombstone events by pulling the object out.  Tombstone events wrap the object in a
	// DeleteFinalStateUnknown struct, so the object needs to be pulled out.
	// Copied from sample-controller
	// This should never happen if we aren't missing events, which we have concluded that we are not
	// and made decisions off of this belief.  Maybe this shouldn't be here?
	if _, ok := obj.(client.Object); !ok {
		// If the object doesn't have Metadata, assume it is a tombstone object of type DeletedFinalStateUnknown
		tombstone, ok := obj.(cgocache.DeletedFinalStateUnknown)
		if !ok {
			log.Error(errors.New("unexpected object type"),
				"Error decoding objects, expected cache.DeletedFinalStateUnknown",
				"received_type", fmt.Sprintf("%T", obj),
				"object", obj)
			return
		}

		// Set DeleteStateUnknown to true
		d.DeleteStateUnknown = true

		// Set obj to the tombstone obj
		obj = tombstone.Obj
	}

	// Pull Object out of the object
	if o, ok := obj.(object); ok {
		d.Object = o
	} else {
		log.Error(errors.New("failed to cast object"),
			"OnDelete missing Object",
			"expected_type", fmt.Sprintf("%T", d.Object),
			"received_type", fmt.Sprintf("%T", obj),
			"object", obj)
		return
	}

	for _, p := range e.predicates {
		if !p.Delete(d) {
			return
		}
	}

	// Invoke delete handler
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	e.handler.Delete(ctx, d, e.queue)
}
