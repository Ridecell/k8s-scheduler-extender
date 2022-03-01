package cache
import (
"time"

	"github.com/ReneKroon/ttlcache/v2"
)
func (c *Cache) getttlCache() *ttlcache.Cache{
	log:=c.Log.WithName("ttl Cache")
	ttlCache := ttlcache.NewCache()
	// it takes 1-2 seconds to schedule a pod on a node, so the indexer doesn’t get updated immediately so need to maintain a temporary cache  for a minute
	err := ttlCache.SetTTL(time.Duration(1 * time.Minute))
	if err != nil {
		log.Error(err, "Failed to create ttl cache")
	}
	return ttlCache
}