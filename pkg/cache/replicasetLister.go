package cache

import (
	"k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/tools/cache"

	appsv1 "k8s.io/api/apps/v1"
)

func (c *Cache) GetReplicaSetLister() v1.ReplicaSetLister {
	replicaSetInformer := c.informerFactory.Apps().V1().ReplicaSets().Informer()
	replicaSetInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(new interface{}) {
			replicaSet, ok := new.(*appsv1.ReplicaSet)
			if !ok {
				c.Log.Info("cannot convert to", "*appsv1.ReplicaSet:", new)
				return
			}
			c.Log.Info("Added", "ReplicaSet:", replicaSet.Name)
		},
		UpdateFunc: func(old, new interface{}) {
			replicaSet, ok := old.(*appsv1.ReplicaSet)
			if !ok {
				c.Log.Info("cannot convert oldObj to", "*appsv1.replicaSet:", old)
				return
			}
			_, ok = new.(*appsv1.ReplicaSet)
			if !ok {
				c.Log.Info("cannot convert newObj to", "*appsv1.replicaSet:", new)
				return
			}
			c.Log.Info("Updated", "Replicaset:", replicaSet.Name)
		},
		DeleteFunc: func(old interface{}) {
			replicaSet, ok := old.(*appsv1.ReplicaSet)
			if !ok {
				c.Log.Info("cannot convert to", "*appsv1.replicaSet:", old)
				return
			}
			c.Log.Info("Deleted", "ReplicaSet:", replicaSet.Name)
		},
	})
	replicaSetLister := c.informerFactory.Apps().V1().ReplicaSets().Lister()
	return replicaSetLister
}
