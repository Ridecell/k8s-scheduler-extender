package cache

import (
	"github.com/ReneKroon/ttlcache/v2"
	"go.uber.org/zap"
	"k8s.io/client-go/tools/cache"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/client-go/listers/apps/v1"
)

// Creates a PodInformer and indexer, watches events and returns informer
func (c *Cache) GetPodInformer(ttlCache *ttlcache.Cache) cache.SharedIndexInformer {
	// watch events
	podInformer := c.InformerFactory.Core().V1().Pods().Informer()
	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(new interface{}) {
			pod, ok := new.(*corev1.Pod)
			if !ok {
				c.Log.Error("cannot convert object to *v1.Pod", zap.Any("object", new))
				return
			}
			c.Log.Debug("Pod Added", zap.String("PodName", pod.Name))
		},
		UpdateFunc: func(old, new interface{}) {
			pod, ok := old.(*corev1.Pod)
			if !ok {
				c.Log.Error("cannot convert oldObj to *v1.Pod", zap.Any("*v1.Pod:", old))
				return
			}
			_, ok = new.(*corev1.Pod)
			if !ok {
				c.Log.Error("cannot convert newObj to *v1.Pod", zap.Any("*v1.Pod:", new))
				return
			}
			c.Log.Debug("Pod Updated", zap.String("PodName", pod.Name), zap.String("NodeName", pod.Spec.NodeName))
		},
		DeleteFunc: func(old interface{}) {
			pod, ok := old.(*corev1.Pod)
			if !ok {
				c.Log.Error("cannot convert to", zap.Any("*v1.Pod", old))
				return
			}
			c.updatettlCache(pod, ttlCache)
			c.Log.Debug("Pod Deleted", zap.String("PodName", pod.Name), zap.String("NodeName", pod.Spec.NodeName))
		},
	})
	//create indexer with index 'nodename'
	err := podInformer.AddIndexers(map[string]cache.IndexFunc{
		"nodename": func(obj interface{}) ([]string, error) {
			var nodeNames []string
			nodeNames = append(nodeNames, obj.(*corev1.Pod).Spec.NodeName)
			return nodeNames, nil
		},
	})
	if err != nil {
		c.Log.Error("Informer error", zap.String("Error", err.Error()))
	}

	return podInformer
}

func (c *Cache) updatettlCache(pod *corev1.Pod, ttlCache *ttlcache.Cache) {
	val, err := ttlCache.Get(pod.Spec.NodeName)
	if err != nil && err != ttlcache.ErrNotFound {
		c.Log.Error("ttlCache", zap.String("Error", err.Error()))
	}
	if val != nil {
		podNames := val.([]string)
		for i, podName := range podNames {
			if pod.Name == podName {
				podNames[i] = podNames[len(podNames)-1]
				podNames = podNames[:len(podNames)-1]
				err = ttlCache.Set(pod.Spec.NodeName, podNames)
				if err != nil {
					c.Log.Error("ttlCache", zap.String("Error", err.Error()))
				}
				c.Log.Info("ttlCache", zap.String("Deleted Pod", podName))
				break
			}
		}
	}
}

// Creates a ReplicaSet informer, watches event and returns a Replicaset lister
func (c *Cache) GetReplicaSetLister() v1.ReplicaSetLister {
	replicaSetInformer := c.InformerFactory.Apps().V1().ReplicaSets().Informer()
	replicaSetInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(new interface{}) {
			replicaSet, ok := new.(*appsv1.ReplicaSet)
			if !ok {
				c.Log.Error("cannot convert to *appsv1.ReplicaSet", zap.Any("Object", new))
				return
			}
			c.Log.Debug("Added ReplicaSet", zap.String("ReplicaSetName", replicaSet.Name))
		},
		UpdateFunc: func(old, new interface{}) {
			replicaSet, ok := old.(*appsv1.ReplicaSet)
			if !ok {
				c.Log.Error("cannot convert oldObj to *appsv1.replicaSet:", zap.Any("Object", old))
				return
			}
			_, ok = new.(*appsv1.ReplicaSet)
			if !ok {
				c.Log.Error("cannot convert newObj to *appsv1.replicaSet:", zap.Any("Object", new))
				return
			}
			c.Log.Debug("Updated ReplicaSet", zap.String("ReplicasetName", replicaSet.Name))
		},
		DeleteFunc: func(old interface{}) {
			replicaSet, ok := old.(*appsv1.ReplicaSet)
			if !ok {
				c.Log.Error("cannot convert to *appsv1.replicaSet", zap.Any("Object", old))
				return
			}
			c.Log.Debug("ReplicaSet Deleted", zap.String("ReplicaSetName", replicaSet.Name))
		},
	})
	replicaSetLister := c.InformerFactory.Apps().V1().ReplicaSets().Lister()
	return replicaSetLister
}
