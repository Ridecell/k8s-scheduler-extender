package cache
import (
	"k8s.io/client-go/tools/cache"

	corev1 "k8s.io/api/core/v1"
)
func (c *Cache) GetPodInformer() cache.SharedIndexInformer {
	// watch events
	podInformer := c.informerFactory.Core().V1().Pods().Informer()
	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(new interface{}) {
			pod, ok := new.(*corev1.Pod)
			if !ok {
				c.Log.Info("cannot convert to *v1.Pod:", new)
				return
			}
			c.Log.Info("Added", "Pod:", pod.Name)
		},
		UpdateFunc: func(old, new interface{}) {
			pod, ok := old.(*corev1.Pod)
			if !ok {
				c.Log.Info("cannot convert oldObj to", "*v1.Pod:", old)
				return
			}
			_, ok = new.(*corev1.Pod)
			if !ok {
				c.Log.Info("cannot convert newObj to", "*v1.Pod:", new)
				return
			}
			c.Log.Info("Updated", "Pod:", pod.Name, "NodeName", pod.Spec.NodeName)
		},
		DeleteFunc: func(old interface{}) {
			pod, ok := old.(*corev1.Pod)
			if !ok {
				c.Log.Info("cannot convert to", "*v1.Pod:", old)
				return
			}
			c.Log.Info("Deleted", "Pod", pod.Name, "NodeName", pod.Spec.NodeName)
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
		c.Log.Error(err, "Informer error")
	}
	return podInformer
}
