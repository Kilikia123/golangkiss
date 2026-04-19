package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

type MainPageCache struct {
	client *goredis.Client
	ttl    time.Duration
}

type CachedPost struct {
	ID        string    `json:"id"`
	AuthorID  string    `json:"author_id"`
	Nickname  string    `json:"nickname"`
	AvatarURL string    `json:"avatar_url"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

func NewMainPageCache(client *goredis.Client, ttl time.Duration) *MainPageCache {
	return &MainPageCache{client: client, ttl: ttl}
}

func (c *MainPageCache) Get(ctx context.Context) ([]CachedPost, bool, error) {
	payload, err := c.client.Get(ctx, mainPageCacheKey()).Result()
	if err == goredis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get main page cache: %w", err)
	}

	var posts []CachedPost
	if err := json.Unmarshal([]byte(payload), &posts); err != nil {
		return nil, false, fmt.Errorf("unmarshal main page cache: %w", err)
	}

	return posts, true, nil
}

func (c *MainPageCache) Set(ctx context.Context, posts []CachedPost) error {
	payload, err := json.Marshal(posts)
	if err != nil {
		return fmt.Errorf("marshal main page cache: %w", err)
	}
	if err := c.client.Set(ctx, mainPageCacheKey(), payload, c.ttl).Err(); err != nil {
		return fmt.Errorf("set main page cache: %w", err)
	}
	return nil
}

func (c *MainPageCache) Invalidate(ctx context.Context) error {
	if err := c.client.Del(ctx, mainPageCacheKey()).Err(); err != nil {
		return fmt.Errorf("invalidate main page cache: %w", err)
	}
	return nil
}

func mainPageCacheKey() string {
	return "posts:feed:offset:0:limit:10"
}
