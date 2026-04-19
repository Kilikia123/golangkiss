package redis

import (
	"context"
	"fmt"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

type LikesStore struct {
	client *goredis.Client
}

type PostLikeInfo struct {
	PostID      string
	LikesCount  uint64
	LikedByUser bool
}

var likePostScript = goredis.NewScript(`
local added = redis.call("SADD", KEYS[1], ARGV[1])
if added == 1 then
  redis.call("INCR", KEYS[2])
end
return added
`)

var unlikePostScript = goredis.NewScript(`
local removed = redis.call("SREM", KEYS[1], ARGV[1])
if removed == 1 then
  local current = tonumber(redis.call("GET", KEYS[2]) or "0")
  if current > 0 then
    redis.call("DECR", KEYS[2])
  end
end
return removed
`)

func NewClient(addr, password string, db int) *goredis.Client {
	return goredis.NewClient(&goredis.Options{
		Addr:         addr,
		Password:     password,
		DB:           db,
		DialTimeout:  3 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	})
}

func NewLikesStore(client *goredis.Client) *LikesStore {
	return &LikesStore{client: client}
}

func (s *LikesStore) Ping(ctx context.Context) error {
	if err := s.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("ping redis: %w", err)
	}
	return nil
}

func (s *LikesStore) LikePost(ctx context.Context, postID, userID string) error {
	if err := likePostScript.Run(ctx, s.client, []string{likedUsersKey(postID), likesCountKey(postID)}, userID).Err(); err != nil {
		return fmt.Errorf("like post: %w", err)
	}
	return nil
}

func (s *LikesStore) UnlikePost(ctx context.Context, postID, userID string) error {
	if err := unlikePostScript.Run(ctx, s.client, []string{likedUsersKey(postID), likesCountKey(postID)}, userID).Err(); err != nil {
		return fmt.Errorf("unlike post: %w", err)
	}
	return nil
}

func (s *LikesStore) DeletePostData(ctx context.Context, postID string) error {
	if err := s.client.Del(ctx, likedUsersKey(postID), likesCountKey(postID)).Err(); err != nil {
		return fmt.Errorf("delete post likes data: %w", err)
	}
	return nil
}

func (s *LikesStore) GetLikeInfo(ctx context.Context, postIDs []string, userID string) (map[string]PostLikeInfo, error) {
	result := make(map[string]PostLikeInfo, len(postIDs))
	if len(postIDs) == 0 {
		return result, nil
	}

	pipe := s.client.Pipeline()
	countCmds := make(map[string]*goredis.StringCmd, len(postIDs))
	likedCmds := make(map[string]*goredis.BoolCmd, len(postIDs))

	for _, postID := range postIDs {
		countCmds[postID] = pipe.Get(ctx, likesCountKey(postID))
		likedCmds[postID] = pipe.SIsMember(ctx, likedUsersKey(postID), userID)
	}

	_, err := pipe.Exec(ctx)
	if err != nil && err != goredis.Nil {
		return nil, fmt.Errorf("get like info: %w", err)
	}

	for _, postID := range postIDs {
		count := uint64(0)
		if raw, getErr := countCmds[postID].Result(); getErr == nil {
			parsed, parseErr := strconv.ParseUint(raw, 10, 64)
			if parseErr != nil {
				return nil, fmt.Errorf("parse likes count for post %s: %w", postID, parseErr)
			}
			count = parsed
		} else if getErr != goredis.Nil {
			return nil, fmt.Errorf("read likes count for post %s: %w", postID, getErr)
		}

		liked, likedErr := likedCmds[postID].Result()
		if likedErr != nil && likedErr != goredis.Nil {
			return nil, fmt.Errorf("read liked flag for post %s: %w", postID, likedErr)
		}

		result[postID] = PostLikeInfo{
			PostID:      postID,
			LikesCount:  count,
			LikedByUser: liked,
		}
	}

	return result, nil
}

func likesCountKey(postID string) string {
	return "post:" + postID + ":likes_count"
}

func likedUsersKey(postID string) string {
	return "post:" + postID + ":liked_users"
}
