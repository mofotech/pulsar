package etcd

import (
	"context"
	"strconv"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// Client wraps the etcd v3 client with helpers used throughout Pulsar.
type Client struct {
	kv    clientv3.KV
	lease clientv3.Lease
	watch clientv3.Watcher
	raw   *clientv3.Client
}

func NewClient(endpoints []string) (*Client, error) {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return &Client{
		kv:    clientv3.NewKV(cli),
		lease: clientv3.NewLease(cli),
		watch: clientv3.NewWatcher(cli),
		raw:   cli,
	}, nil
}

func (c *Client) Close() {
	c.raw.Close()
}

// Put stores a key-value pair without expiry.
func (c *Client) Put(ctx context.Context, key, value string) error {
	_, err := c.kv.Put(ctx, key, value)
	return err
}

// PutWithTTL stores a key-value pair with a time-to-live (seconds).
func (c *Client) PutWithTTL(ctx context.Context, key, value string, ttlSeconds int64) error {
	resp, err := c.lease.Grant(ctx, ttlSeconds)
	if err != nil {
		return err
	}
	_, err = c.kv.Put(ctx, key, value, clientv3.WithLease(resp.ID))
	return err
}

// Get retrieves the value for a key. Returns ("", nil) if not found.
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	resp, err := c.kv.Get(ctx, key)
	if err != nil {
		return "", err
	}
	if len(resp.Kvs) == 0 {
		return "", nil
	}
	return string(resp.Kvs[0].Value), nil
}

// GetPrefix retrieves all key-value pairs with the given prefix.
func (c *Client) GetPrefix(ctx context.Context, prefix string) (map[string]string, error) {
	resp, err := c.kv.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		result[string(kv.Key)] = string(kv.Value)
	}
	return result, nil
}

// Delete removes a key.
func (c *Client) Delete(ctx context.Context, key string) error {
	_, err := c.kv.Delete(ctx, key)
	return err
}

// PutIfAbsent stores key=value only if the key does not already exist.
// Returns (true, nil) if the key was created, (false, nil) if it already existed.
func (c *Client) PutIfAbsent(ctx context.Context, key, value string) (bool, error) {
	resp, err := c.kv.Txn(ctx).
		If(clientv3.Compare(clientv3.Version(key), "=", 0)).
		Then(clientv3.OpPut(key, value)).
		Commit()
	if err != nil {
		return false, err
	}
	return resp.Succeeded, nil
}

// IncrCounter atomically increments a counter at key, initializing to start+1 if absent.
// Retries on CAS conflict. Returns the new value.
func (c *Client) IncrCounter(ctx context.Context, key string, start int64) (int64, error) {
	for {
		resp, err := c.kv.Get(ctx, key)
		if err != nil {
			return 0, err
		}
		var current int64 = start
		var modRev int64
		if len(resp.Kvs) > 0 {
			current, _ = strconv.ParseInt(string(resp.Kvs[0].Value), 10, 64)
			modRev = resp.Kvs[0].ModRevision
		}
		next := current + 1
		nextStr := strconv.FormatInt(next, 10)
		var txnResp *clientv3.TxnResponse
		if modRev == 0 {
			txnResp, err = c.kv.Txn(ctx).
				If(clientv3.Compare(clientv3.Version(key), "=", 0)).
				Then(clientv3.OpPut(key, nextStr)).
				Commit()
		} else {
			txnResp, err = c.kv.Txn(ctx).
				If(clientv3.Compare(clientv3.ModRevision(key), "=", modRev)).
				Then(clientv3.OpPut(key, nextStr)).
				Commit()
		}
		if err != nil {
			return 0, err
		}
		if txnResp.Succeeded {
			return next, nil
		}
		// Retry on conflict
	}
}

// WatchEvent is a single etcd watch notification.
type WatchEvent struct {
	Type  string // "PUT" or "DELETE"
	Key   string
	Value string
}

// Watch streams events for all keys with the given prefix until ctx is cancelled.
func (c *Client) Watch(ctx context.Context, prefix string) <-chan WatchEvent {
	out := make(chan WatchEvent, 64)
	wch := c.watch.Watch(ctx, prefix, clientv3.WithPrefix())
	go func() {
		defer close(out)
		for resp := range wch {
			for _, ev := range resp.Events {
				we := WatchEvent{Key: string(ev.Kv.Key), Value: string(ev.Kv.Value)}
				if ev.Type == clientv3.EventTypeDelete {
					we.Type = "DELETE"
				} else {
					we.Type = "PUT"
				}
				select {
				case out <- we:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}
