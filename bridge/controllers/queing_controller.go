package controllers

import (
	"fmt"
	"time"

	"github.com/golang/glog"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

type ControllerHandler interface {
	GetTypeNamePlural() string
	GetObjectType() runtime.Object
	Handle(string, interface{})
}

type Controller struct {
	indexer  cache.Indexer
	queue    workqueue.RateLimitingInterface
	informer cache.Controller
	handler  *ControllerHandler
}

func NewController(queue workqueue.RateLimitingInterface, indexer cache.Indexer, informer cache.Controller, handler *ControllerHandler) *Controller {
	return &Controller{
		informer: informer,
		indexer:  indexer,
		queue:    queue,
		handler:  handler,
	}
}

func (c *Controller) processNextItem() bool {
	// Wait until there is a new item in the working queue
	key, quit := c.queue.Get()
	if quit {
		return false
	}
	// Tell the queue that we are done with processing this key. This unblocks the key for other workers
	// This allows safe parallel processing because two objectss with the same key are never processed in
	// parallel.
	defer c.queue.Done(key)

	// Invoke the method containing the business logic
	err := c.handle(key.(string))
	// Handle the error if something went wrong during the execution of the business logic
	c.handleErr(err, key)
	return true
}

// handle calls the business logic of the controller. In this controller it simply prints
// information about the objects to stdout. In case an error happened, it has to simply return the error.
// The retry logic should not be part of the business logic.
func (c *Controller) handle(key string) error {
	obj, exists, err := c.indexer.GetByKey(key)
	if err != nil {
		glog.Errorf("Fetching object with key %s from store failed with %v", key, err)
		return err
	}
	if !exists {
		(*c.handler).Handle(key, nil)
	} else {
		(*c.handler).Handle(key, obj)
	}
	return nil
}

// handleErr checks if an error happened and makes sure we will retry later.
func (c *Controller) handleErr(err error, key interface{}) {
	if err == nil {
		// Forget about the #AddRateLimited history of the key on every successful synchronization.
		// This ensures that future processing of updates for this key is not delayed because of
		// an outdated error history.
		c.queue.Forget(key)
		return
	}

	// This controller retries 5 times if something goes wrong. After that, it stops trying.
	if c.queue.NumRequeues(key) < 5 {
		glog.Infof("Error syncing %s %v: %v", (*c.handler).GetTypeNamePlural(), key, err)

		// Re-enqueue the key rate limited. Based on the rate limiter on the
		// queue and the re-enqueue history, the key will be processed later again.
		c.queue.AddRateLimited(key)
		return
	}

	c.queue.Forget(key)
	// Report to an external entity that, even after several retries, we could not successfully process this key
	utilruntime.HandleError(err)
	glog.Infof("Dropping %s %q out of the queue: %v", (*c.handler).GetTypeNamePlural(), key, err)
}

func (c *Controller) Run(threadiness int, stopCh chan struct{}) {
	defer utilruntime.HandleCrash()

	// Let the workers stop when we are done
	defer c.queue.ShutDown()
	glog.Infof("Starting controller for %s.", (*c.handler).GetTypeNamePlural())

	go c.informer.Run(stopCh)

	// Wait for all involved caches to be synced, before processing items from the queue is started
	if !cache.WaitForCacheSync(stopCh, c.informer.HasSynced) {
		utilruntime.HandleError(fmt.Errorf("Timed out waiting for caches to sync"))
		return
	}

	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	<-stopCh
	glog.Info("Stopping controller for %s.", (*c.handler).GetTypeNamePlural())
}

func (c *Controller) runWorker() {
	for c.processNextItem() {
	}
}

func CreateController(clientset *kubernetes.Clientset, handler interface{}) *Controller {

	h, ok := handler.(ControllerHandler)
	if !ok {
		glog.Fatal("Incorrect handler interface. Expecting a ControllerHandler")
	}

	// create the object watcher
	objListWatcher := cache.NewListWatchFromClient(clientset.CoreV1().RESTClient(), h.GetTypeNamePlural(), "", fields.Everything())

	// create the workqueue
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())

	// Bind the workqueue to a cache with the help of an informer. This way we make sure that
	// whenever the cache is updated, the objects key is added to the workqueue.
	// Note that when we finally process the item from the workqueue, we might see a newer version
	// of the Object than the version which was responsible for triggering the update.
	indexer, informer := cache.NewIndexerInformer(objListWatcher, h.GetObjectType(), 0, cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err == nil {
				queue.Add(key)
			}
		},
		UpdateFunc: func(old interface{}, new interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(new)
			if err == nil {
				queue.Add(key)
			}
		},
		DeleteFunc: func(obj interface{}) {
			// IndexerInformer uses a delta queue, therefore for deletes we have to use this
			// key function.
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err == nil {
				queue.Add(key)
			}
		},
	}, cache.Indexers{})

	controller := NewController(queue, indexer, informer, &h)

	return controller
}
