package service

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	blogv1 "github.com/<твой-github>/blog/gen/proto/blog/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type storedPost struct {
	ID        string
	AuthorID  string
	Nickname  string
	AvatarURL string
	Body      string
	CreatedAt time.Time
	LikedBy   map[string]struct{}
}

type BlogService struct {
	blogv1.UnimplementedBlogServiceServer

	mu      sync.RWMutex
	posts   []*storedPost
	nextID  int64
}

func NewBlogService() *BlogService {
	now := time.Now().UTC()

	return &BlogService{
		posts: []*storedPost{
			{
				ID:        "1",
				AuthorID:  "101",
				Nickname:  "nikita",
				AvatarURL: "https://example.com/avatar1.jpg",
				Body:      "Первый пост в блоге",
				CreatedAt: now.Add(-2 * time.Hour),
				LikedBy:   map[string]struct{}{"777": {}},
			},
			{
				ID:        "2",
				AuthorID:  "102",
				Nickname:  "masha",
				AvatarURL: "https://example.com/avatar2.jpg",
				Body:      "Второй пост в блоге",
				CreatedAt: now.Add(-1 * time.Hour),
				LikedBy:   map[string]struct{}{},
			},
		},
		nextID: 3,
	}
}

func userIDFromContext(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "missing metadata")
	}

	values := md.Get("x-user-id")
	if len(values) == 0 || values[0] == "" {
		return "", status.Error(codes.Unauthenticated, "missing x-user-id header")
	}

	return values[0], nil
}

func toProtoPost(p *storedPost, currentUserID string) *blogv1.Post {
	_, liked := p.LikedBy[currentUserID]

	return &blogv1.Post{
		Id: p.ID,
		Author: &blogv1.Author{
			Id:        p.AuthorID,
			Nickname:  p.Nickname,
			AvatarUrl: p.AvatarURL,
		},
		Body:       p.Body,
		CreatedAt:  p.CreatedAt.Format(time.RFC3339),
		LikesCount: uint64(len(p.LikedBy)),
		LikedByMe:  liked,
	}
}

func (s *BlogService) GetPosts(ctx context.Context, req *blogv1.GetPostsRequest) (*blogv1.GetPostsResponse, error) {
	userID, err := userIDFromContext(ctx)
	if err != nil {
		return nil, err
	}

	limit := int(req.GetLimit())
	offset := int(req.GetOffset())

	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	postsCopy := make([]*storedPost, len(s.posts))
	copy(postsCopy, s.posts)

	sort.Slice(postsCopy, func(i, j int) bool {
		return postsCopy[i].CreatedAt.After(postsCopy[j].CreatedAt)
	})

	if offset >= len(postsCopy) {
		return &blogv1.GetPostsResponse{Posts: []*blogv1.Post{}}, nil
	}

	end := offset + limit
	if end > len(postsCopy) {
		end = len(postsCopy)
	}

	result := make([]*blogv1.Post, 0, end-offset)
	for _, p := range postsCopy[offset:end] {
		result = append(result, toProtoPost(p, userID))
	}

	return &blogv1.GetPostsResponse{Posts: result}, nil
}

func (s *BlogService) CreatePost(ctx context.Context, req *blogv1.CreatePostRequest) (*blogv1.Post, error) {
	userID, err := userIDFromContext(ctx)
	if err != nil {
		return nil, err
	}

	if req.GetBody() == "" {
		return nil, status.Error(codes.InvalidArgument, "body is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	id := strconv.FormatInt(s.nextID, 10)
	s.nextID++

	post := &storedPost{
		ID:        id,
		AuthorID:  userID,
		Nickname:  "user_" + userID,
		AvatarURL: "https://example.com/default-avatar.jpg",
		Body:      req.GetBody(),
		CreatedAt: time.Now().UTC(),
		LikedBy:   map[string]struct{}{},
	}

	s.posts = append(s.posts, post)

	return toProtoPost(post, userID), nil
}

func (s *BlogService) UpdatePost(ctx context.Context, req *blogv1.UpdatePostRequest) (*blogv1.Post, error) {
	userID, err := userIDFromContext(ctx)
	if err != nil {
		return nil, err
	}

	if req.GetPostId() == "" {
		return nil, status.Error(codes.InvalidArgument, "post_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range s.posts {
		if p.ID == req.GetPostId() {
			if p.AuthorID != userID {
				return nil, status.Error(codes.PermissionDenied, "you can edit only your own posts")
			}
			p.Body = req.GetBody()
			return toProtoPost(p, userID), nil
		}
	}

	return nil, status.Error(codes.NotFound, "post not found")
}

func (s *BlogService) DeletePost(ctx context.Context, req *blogv1.DeletePostRequest) (*blogv1.Empty, error) {
	userID, err := userIDFromContext(ctx)
	if err != nil {
		return nil, err
	}

	if req.GetPostId() == "" {
		return nil, status.Error(codes.InvalidArgument, "post_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for i, p := range s.posts {
		if p.ID == req.GetPostId() {
			if p.AuthorID != userID {
				return nil, status.Error(codes.PermissionDenied, "you can delete only your own posts")
			}
			s.posts = append(s.posts[:i], s.posts[i+1:]...)
			return &blogv1.Empty{}, nil
		}
	}

	return nil, status.Error(codes.NotFound, "post not found")
}

func (s *BlogService) LikePost(ctx context.Context, req *blogv1.LikePostRequest) (*blogv1.Empty, error) {
	userID, err := userIDFromContext(ctx)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range s.posts {
		if p.ID == req.GetPostId() {
			p.LikedBy[userID] = struct{}{}
			return &blogv1.Empty{}, nil
		}
	}

	return nil, status.Error(codes.NotFound, "post not found")
}

func (s *BlogService) UnlikePost(ctx context.Context, req *blogv1.UnlikePostRequest) (*blogv1.Empty, error) {
	userID, err := userIDFromContext(ctx)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range s.posts {
		if p.ID == req.GetPostId() {
			delete(p.LikedBy, userID)
			return &blogv1.Empty{}, nil
		}
	}

	return nil, status.Error(codes.NotFound, "post not found")
}

func (s *BlogService) DebugString() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fmt.Sprintf("posts=%d", len(s.posts))
}
