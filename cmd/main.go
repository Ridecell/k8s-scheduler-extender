package main

import (
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"ridecell-k8s-scheduler-extender/pkg/cache"
	"time"

	"github.com/ReneKroon/ttlcache/v2"
	"github.com/go-logr/logr"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	kubernetes "k8s.io/client-go/kubernetes"
	clientCache "k8s.io/client-go/tools/cache"
	clientcmd "k8s.io/client-go/tools/clientcmd"
)

var logger logr.Logger

func connectToK8s(logger logr.Logger) *kubernetes.Clientset {
	home, exists := os.LookupEnv("HOME")
	if !exists {
		home = "/root"
	}

	configPath := filepath.Join(home, ".kube", "config")

	config, err := clientcmd.BuildConfigFromFlags("", configPath)
	if err != nil {
		config, err = rest.InClusterConfig()
		if err != nil {
			logger.Error(err, "failed to create K8s config")
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		logger.Error(err, "Failed to create K8s clientset")
	}

	return clientset
}
func createLogger() logr.Logger {
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	logger = zap.New(zap.UseFlagOptions(&opts))
	return logger
}
func main() {
    //init zap logger
	logger := createLogger()
    log:=logger.WithName("Main")
	//init k8s client
	clientset := connectToK8s(log)
    
	//init temprory cache
	customCache := ttlcache.NewCache()
	err:=customCache.SetTTL(time.Duration(1 * time.Minute))
	if err !=nil{
		log.Error(err,"Custom cache error")
	}

	// setup k8s informer factory
	informerFactory := informers.NewSharedInformerFactory(clientset, 10*time.Minute)
	b := cache.BaseHandler(informerFactory, customCache, logger)
	stopCh := make(chan struct{})
	informerFactory.Start(stopCh)
	clientCache.WaitForCacheSync(stopCh)


	http.HandleFunc("/", b.Index)
	http.HandleFunc("/foo/filter", b.FooFilter)
	s := &http.Server{
		Addr: ":8080",
	}

	if err := s.ListenAndServe(); err != nil {
		log.Error(err, "Server error")
	}
}
