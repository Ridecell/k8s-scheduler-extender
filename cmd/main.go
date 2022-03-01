package main

import (
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"ridecell-k8s-scheduler-extender/pkg/routes"
	"time"
	// "ridecell-k8s-scheduler-extender/pkg/cache"

	"github.com/go-logr/logr"
	"go.uber.org/zap/zapcore"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	kubernetes "k8s.io/client-go/kubernetes"
	clientCache "k8s.io/client-go/tools/cache"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	podspernode "ridecell-k8s-scheduler-extender/pkg/routes/podspernode"
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
		TimeEncoder: zapcore.RFC3339TimeEncoder,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	logger = zap.New(zap.UseFlagOptions(&opts))
	return logger
}
func main() {
	//init zap logger
	logger := createLogger()
	log := logger.WithName("Main")

	//init k8s client
	clientset := connectToK8s(log)

	// setup k8s informer factory
	informerFactory := informers.NewSharedInformerFactory(clientset, 10*time.Minute)
	// c := cache.NewInformerCache(informerFactory, logger)

	p := podspernode.NewPodsPerNode(informerFactory, logger)

	stopCh := make(chan struct{})
	informerFactory.Start(stopCh)
	clientCache.WaitForCacheSync(stopCh)

	routes.BaseHandler()
	routes.PodsPerNodeHandler(p)

	s := &http.Server{
		Addr: ":8080",
	}

	if err := s.ListenAndServe(); err != nil {
		log.Error(err, "Server error")
	}
}
