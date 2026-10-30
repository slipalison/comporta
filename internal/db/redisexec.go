package db

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/redis/go-redis/v9"
)

// RedisResult is what a Redis command returned, converted for JSON.
type RedisResult struct {
	Value     any  `json:"value"`
	Items     int  `json:"items,omitempty"`
	Truncated bool `json:"truncated,omitempty"`
}

// RunRedis sends one command. Lists and maps longer than maxItems are cut,
// and long strings are truncated like SQL cells.
func RunRedis(ctx context.Context, client *redis.Client, command string, args []string, maxItems int) (*RedisResult, error) {
	cmdArgs := make([]any, 0, len(args)+1)
	cmdArgs = append(cmdArgs, command)
	for _, a := range args {
		cmdArgs = append(cmdArgs, a)
	}
	v, err := client.Do(ctx, cmdArgs...).Result()
	if errors.Is(err, redis.Nil) {
		return &RedisResult{Value: nil}, nil
	}
	if err != nil {
		return nil, err
	}
	res := &RedisResult{}
	res.Value = redisValue(v, maxItems, res)
	return res, nil
}

// redisValue converts RESP2 and RESP3 replies. The first collection longer
// than maxItems marks the result as truncated.
func redisValue(v any, maxItems int, res *RedisResult) any {
	switch x := v.(type) {
	case nil, int64, float64, bool:
		return x
	case string:
		return truncate(x)
	case []any:
		res.Items = max(res.Items, len(x))
		if len(x) > maxItems {
			x, res.Truncated = x[:maxItems], true
		}
		out := make([]any, len(x))
		for i, it := range x {
			out[i] = redisValue(it, maxItems, res)
		}
		return out
	case map[any]any:
		res.Items = max(res.Items, len(x))
		keys := make([]string, 0, len(x))
		byKey := make(map[string]any, len(x))
		for k, val := range x {
			ks := fmt.Sprint(k)
			keys = append(keys, ks)
			byKey[ks] = val
		}
		sort.Strings(keys)
		if len(keys) > maxItems {
			keys, res.Truncated = keys[:maxItems], true
		}
		out := make(map[string]any, len(keys))
		for _, k := range keys {
			out[k] = redisValue(byKey[k], maxItems, res)
		}
		return out
	case error:
		return x.Error()
	default:
		return truncate(fmt.Sprint(x))
	}
}

// RedisKey is one sampled key.
type RedisKey struct {
	Key  string `json:"key"`
	Type string `json:"type"`
	TTL  int64  `json:"ttl_seconds"` // -1: no expiry
}

// RedisSchema is what describe_schema returns for Redis.
type RedisSchema struct {
	DBSize  int64      `json:"total_keys"`
	Pattern string     `json:"pattern"`
	Sampled []RedisKey `json:"sampled_keys"`
}

// DescribeRedis samples up to n keys matching pattern with SCAN (never KEYS),
// with their type and time to live.
func DescribeRedis(ctx context.Context, client *redis.Client, pattern string, n int) (*RedisSchema, error) {
	if pattern == "" {
		pattern = "*"
	}
	size, err := client.DBSize(ctx).Result()
	if err != nil {
		return nil, err
	}
	keys, err := scanKeys(ctx, client, pattern, n)
	if err != nil {
		return nil, err
	}
	sampled, err := keyDetails(ctx, client, keys)
	if err != nil {
		return nil, err
	}
	return &RedisSchema{DBSize: size, Pattern: pattern, Sampled: sampled}, nil
}

// scanKeys returns up to n keys, sorted, in a bounded number of SCAN rounds:
// a sparse pattern must not walk the whole keyspace.
func scanKeys(ctx context.Context, client *redis.Client, pattern string, n int) ([]string, error) {
	var keys []string
	var cursor uint64
	for range 20 {
		batch, next, err := client.Scan(ctx, cursor, pattern, int64(n)).Result()
		if err != nil {
			return nil, err
		}
		keys, cursor = append(keys, batch...), next
		if cursor == 0 || len(keys) >= n {
			break
		}
	}
	if len(keys) > n {
		keys = keys[:n]
	}
	sort.Strings(keys)
	return keys, nil
}

// keyDetails fetches TYPE and TTL of every key in one pipeline.
func keyDetails(ctx context.Context, client *redis.Client, keys []string) ([]RedisKey, error) {
	out := make([]RedisKey, 0, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	pipe := client.Pipeline()
	types := make([]*redis.StatusCmd, len(keys))
	ttls := make([]*redis.DurationCmd, len(keys))
	for i, k := range keys {
		types[i] = pipe.Type(ctx, k)
		ttls[i] = pipe.TTL(ctx, k)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	for i, k := range keys {
		ttl := int64(-1)
		if d := ttls[i].Val(); d > 0 {
			ttl = int64(d.Seconds())
		}
		out = append(out, RedisKey{Key: k, Type: types[i].Val(), TTL: ttl})
	}
	return out, nil
}
