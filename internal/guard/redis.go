package guard

import (
	"errors"
	"fmt"
	"strings"

	"github.com/slipalison/comporta/internal/model"
)

// Redis commands by the permission they need. A command missing from every
// list needs DDL/admin, as an unknown SQL statement does.
var (
	redisReads = set(
		"GET", "MGET", "GETRANGE", "STRLEN", "EXISTS", "TYPE", "TTL", "PTTL", "EXPIRETIME", "PEXPIRETIME",
		"HGET", "HMGET", "HGETALL", "HKEYS", "HVALS", "HLEN", "HEXISTS", "HSTRLEN", "HRANDFIELD",
		"LRANGE", "LLEN", "LINDEX", "LPOS",
		"SMEMBERS", "SISMEMBER", "SMISMEMBER", "SCARD", "SRANDMEMBER", "SINTER", "SUNION", "SDIFF", "SINTERCARD",
		"ZRANGE", "ZRANGEBYSCORE", "ZRANGEBYLEX", "ZREVRANGE", "ZREVRANGEBYSCORE", "ZREVRANGEBYLEX",
		"ZSCORE", "ZMSCORE", "ZCARD", "ZCOUNT", "ZLEXCOUNT", "ZRANK", "ZREVRANK", "ZRANDMEMBER",
		"XRANGE", "XREVRANGE", "XLEN", "XINFO",
		"SCAN", "HSCAN", "SSCAN", "ZSCAN", "RANDOMKEY", "DBSIZE", "OBJECT", "MEMORY",
		"PFCOUNT", "GETBIT", "BITCOUNT", "BITPOS", "GEOPOS", "GEODIST", "GEOHASH", "GEOSEARCH",
		"JSON.GET", "JSON.MGET", "JSON.TYPE", "JSON.STRLEN", "JSON.ARRLEN", "JSON.OBJKEYS", "JSON.OBJLEN",
		"PING", "ECHO", "TIME", "INFO", "LASTSAVE",
	)
	// Commands that only create data that was not there.
	redisInserts = set("SETNX", "MSETNX", "HSETNX", "LPUSH", "RPUSH", "LPUSHX", "RPUSHX", "LINSERT",
		"SADD", "ZADD", "XADD", "PFADD", "GEOADD")
	// Commands that change existing data.
	redisUpdates = set("INCR", "DECR", "INCRBY", "DECRBY", "INCRBYFLOAT", "HINCRBY", "HINCRBYFLOAT", "ZINCRBY",
		"APPEND", "SETRANGE", "LSET", "SETBIT", "EXPIRE", "PEXPIRE", "EXPIREAT", "PEXPIREAT", "PERSIST",
		"RENAME", "RENAMENX", "JSON.NUMINCRBY", "JSON.STRAPPEND", "JSON.ARRAPPEND", "JSON.ARRINSERT", "JSON.TOGGLE")
	// Commands that may create a key or overwrite one.
	redisUpserts = set("SET", "MSET", "HSET", "HMSET", "SETEX", "PSETEX", "GETSET", "JSON.SET", "COPY",
		"SMOVE", "LMOVE", "RPOPLPUSH", "SUNIONSTORE", "SINTERSTORE", "SDIFFSTORE", "ZUNIONSTORE", "ZINTERSTORE",
		"ZDIFFSTORE", "ZRANGESTORE", "GEOSEARCHSTORE")
	redisDeletes = set("DEL", "UNLINK", "GETDEL", "HDEL", "LPOP", "RPOP", "LREM", "LTRIM", "SREM", "SPOP",
		"ZREM", "ZREMRANGEBYSCORE", "ZREMRANGEBYRANK", "ZREMRANGEBYLEX", "ZPOPMIN", "ZPOPMAX",
		"XDEL", "XTRIM", "JSON.DEL", "JSON.FORGET", "JSON.ARRPOP", "JSON.ARRTRIM", "JSON.CLEAR")
	// Never allowed: they block the connection, change which database or
	// transaction the shared pool is in, stream forever, or read every key.
	redisForbidden = map[string]string{
		"KEYS":       "KEYS scans every key and blocks the server; use SCAN with MATCH and COUNT",
		"SELECT":     "SELECT would switch the database of a pooled connection; put the database number in the connection string",
		"SWAPDB":     "SWAPDB swaps whole databases",
		"MULTI":      "transactions are not available; send one command per call",
		"EXEC":       "transactions are not available; send one command per call",
		"DISCARD":    "transactions are not available; send one command per call",
		"WATCH":      "transactions are not available; send one command per call",
		"UNWATCH":    "transactions are not available; send one command per call",
		"SUBSCRIBE":  "subscriptions stream forever and are not available",
		"PSUBSCRIBE": "subscriptions stream forever and are not available",
		"SSUBSCRIBE": "subscriptions stream forever and are not available",
		"MONITOR":    "MONITOR streams forever and is not available",
		"BLPOP":      "blocking commands are not available",
		"BRPOP":      "blocking commands are not available",
		"BLMOVE":     "blocking commands are not available",
		"BLMPOP":     "blocking commands are not available",
		"BZPOPMIN":   "blocking commands are not available",
		"BZPOPMAX":   "blocking commands are not available",
		"BZMPOP":     "blocking commands are not available",
		"BRPOPLPUSH": "blocking commands are not available",
		"WAIT":       "blocking commands are not available",
		"WAITAOF":    "blocking commands are not available",
		"QUIT":       "QUIT would close a pooled connection",
		"RESET":      "RESET would reset a pooled connection",
		"AUTH":       "authenticate in the connection string, not per call",
		"HELLO":      "HELLO would change the protocol of a pooled connection",
		"SHUTDOWN":   "SHUTDOWN stops the server",
		"DEBUG":      "DEBUG can crash or stall the server",
		"SYNC":       "replication commands are not available",
		"PSYNC":      "replication commands are not available",
		"REPLICAOF":  "replication commands are not available",
		"SLAVEOF":    "replication commands are not available",
		"CLIENT":     "CLIENT changes or kills connections",
		"FAILOVER":   "FAILOVER is not available",
		"MIGRATE":    "MIGRATE moves keys to another server",
		"XREAD":      "XREAD with BLOCK waits; use XRANGE",
		"XREADGROUP": "consumer groups change server state; use XRANGE",
	}
)

func set(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, it := range items {
		m[it] = true
	}
	return m
}

// AnalyzeRedis returns the operations a Redis command needs, from the command
// name alone (module commands like JSON.SET included).
func AnalyzeRedis(command string) (Analysis, error) {
	cmd := strings.ToUpper(strings.TrimSpace(command))
	if cmd == "" {
		return Analysis{}, errors.New("empty Redis command")
	}
	if strings.ContainsAny(cmd, " \t\r\n") {
		return Analysis{}, fmt.Errorf("pass only the command name in `command` and its arguments in `args` (got %q)", command)
	}
	if why, ok := redisForbidden[cmd]; ok {
		return Analysis{}, fmt.Errorf("%s is not allowed: %s", cmd, why)
	}
	a := Analysis{Leading: cmd, ReturnsRows: true}
	switch {
	case redisReads[cmd]:
	case redisInserts[cmd]:
		a.Ops = []model.Op{model.OpInsert}
	case redisUpdates[cmd]:
		a.Ops = []model.Op{model.OpUpdate}
	case redisUpserts[cmd]:
		a.Ops = []model.Op{model.OpInsert, model.OpUpdate}
	case redisDeletes[cmd]:
		a.Ops = []model.Op{model.OpDelete}
	default: // FLUSHDB, FLUSHALL, CONFIG, EVAL, FUNCTION, SCRIPT, ACL, MODULE, SAVE, RESTORE, ... or unknown
		a.Ops = []model.Op{model.OpDDL}
	}
	return a, nil
}
