package cache

import (
	"go.uber.org/zap"
	"k8s.io/client-go/informers"
)

type Cache struct {
	Log             *zap.Logger
	InformerFactory informers.SharedInformerFactory
}
