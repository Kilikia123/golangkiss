package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	blogv1 "golangkiss/gen/proto/blog/v1"
	"golangkiss/internal/storage/postgres"
	rediscache "golangkiss/internal/storage/redis"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type BlogService struct {
	blogv1.UnimplementedBlogServiceServer

	repo      *postgres.PostRepository
	likes     *rediscache.LikesStore
	mainCache *rediscache.MainPageCache
}

func NewBlogService(repo *postgres.PostRepository, likes *rediscache.LikesStore, mainCache *rediscache.MainPageCache) *BlogService {
	return &BlogService{
		repo:      repo,
		likes:     likes,
		mainCache: mainCache,
	}
}

func userIDFromContext(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "missing metadata")
	}

	values := md.Get("x-user-id")
	if len(values) == 0 || strings.TrimSpace(values[0]) == "" {
		return "", status.Error(codes.Unauthenticated, "missing x-user-id header")
	}

	return values[0], nil
}

func toProtoPost(post rediscache.CachedPost, likesInfo rediscache.PostLikeInfo) *blogv1.Post {
	return &blogv1.Post{
		Id: post.ID,
		Author: &blogv1.Author{
			Id:        post.AuthorID,
			Nickname:  post.Nickname,
			AvatarUrl: post.AvatarURL,
		},
		Body:       post.Body,
		CreatedAt:  post.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		LikesCount: likesInfo.LikesCount,
		LikedByMe:  likesInfo.LikedByUser,
	}
}

func toCachedPost(post postgres.Post) rediscache.CachedPost {
	return rediscache.CachedPost{
		ID:        strconv.FormatUint(post.ID, 10),
		AuthorID:  post.AuthorID,
		Nickname:  post.Nickname,
		AvatarURL: post.AvatarURL,
		Body:      post.Body,
		CreatedAt: post.CreatedAt,
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

	var posts []rediscache.CachedPost
	if offset == 0 && limit == 10 {
		cachedPosts, found, cacheErr := s.mainCache.Get(ctx)
		if cacheErr != nil {
			return nil, status.Errorf(codes.Internal, "main page cache error: %v", cacheErr)
		}
		if found {
			posts = cachedPosts
		} else {
			dbPosts, repoErr := s.repo.ListPosts(ctx, limit, offset)
			if repoErr != nil {
				return nil, status.Errorf(codes.Internal, "list posts: %v", repoErr)
			}

			posts = make([]rediscache.CachedPost, 0, len(dbPosts))
			for _, post := range dbPosts {
				posts = append(posts, toCachedPost(post))
			}

			if setErr := s.mainCache.Set(ctx, posts); setErr != nil {
				return nil, status.Errorf(codes.Internal, "save main page cache: %v", setErr)
			}
		}
	} else {
		dbPosts, repoErr := s.repo.ListPosts(ctx, limit, offset)
		if repoErr != nil {
			return nil, status.Errorf(codes.Internal, "list posts: %v", repoErr)
		}

		posts = make([]rediscache.CachedPost, 0, len(dbPosts))
		for _, post := range dbPosts {
			posts = append(posts, toCachedPost(post))
		}
	}

	postIDs := make([]string, 0, len(posts))
	for _, post := range posts {
		postIDs = append(postIDs, post.ID)
	}

	likesInfo, err := s.likes.GetLikeInfo(ctx, postIDs, userID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "read likes from redis: %v", err)
	}

	response := &blogv1.GetPostsResponse{Posts: make([]*blogv1.Post, 0, len(posts))}
	for _, post := range posts {
		response.Posts = append(response.Posts, toProtoPost(post, likesInfo[post.ID]))
	}

	return response, nil
}

func (s *BlogService) CreatePost(ctx context.Context, req *blogv1.CreatePostRequest) (*blogv1.Post, error) {
	userID, err := userIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetBody()) == "" {
		return nil, status.Error(codes.InvalidArgument, "body is required")
	}

	post := &postgres.Post{
		AuthorID:  userID,
		Nickname:  "user_" + userID,
		AvatarURL: "https://example.com/default-avatar.jpg",
		Body:      req.GetBody(),
	}
	if err := s.repo.CreatePost(ctx, post); err != nil {
		return nil, status.Errorf(codes.Internal, "create post: %v", err)
	}
	if err := s.mainCache.Invalidate(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "invalidate main page cache: %v", err)
	}

	cached := toCachedPost(*post)
	return toProtoPost(cached, rediscache.PostLikeInfo{PostID: cached.ID}), nil
}

func (s *BlogService) UpdatePost(ctx context.Context, req *blogv1.UpdatePostRequest) (*blogv1.Post, error) {
	userID, err := userIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if req.GetPostId() == "" {
		return nil, status.Error(codes.InvalidArgument, "post_id is required")
	}
	if strings.TrimSpace(req.GetBody()) == "" {
		return nil, status.Error(codes.InvalidArgument, "body is required")
	}

	post, err := s.repo.GetPostByID(ctx, req.GetPostId())
	if err != nil {
		if err == postgres.ErrPostNotFound {
			return nil, status.Error(codes.NotFound, "post not found")
		}
		return nil, status.Errorf(codes.Internal, "get post: %v", err)
	}
	if post.AuthorID != userID {
		return nil, status.Error(codes.PermissionDenied, "you can edit only your own posts")
	}

	updated, err := s.repo.UpdatePostBody(ctx, req.GetPostId(), req.GetBody())
	if err != nil {
		if err == postgres.ErrPostNotFound {
			return nil, status.Error(codes.NotFound, "post not found")
		}
		return nil, status.Errorf(codes.Internal, "update post: %v", err)
	}
	if err := s.mainCache.Invalidate(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "invalidate main page cache: %v", err)
	}

	likeInfo, err := s.likes.GetLikeInfo(ctx, []string{req.GetPostId()}, userID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "read likes from redis: %v", err)
	}

	cached := toCachedPost(*updated)
	return toProtoPost(cached, likeInfo[cached.ID]), nil
}

func (s *BlogService) DeletePost(ctx context.Context, req *blogv1.DeletePostRequest) (*blogv1.Empty, error) {
	userID, err := userIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if req.GetPostId() == "" {
		return nil, status.Error(codes.InvalidArgument, "post_id is required")
	}

	post, err := s.repo.GetPostByID(ctx, req.GetPostId())
	if err != nil {
		if err == postgres.ErrPostNotFound {
			return nil, status.Error(codes.NotFound, "post not found")
		}
		return nil, status.Errorf(codes.Internal, "get post: %v", err)
	}
	if post.AuthorID != userID {
		return nil, status.Error(codes.PermissionDenied, "you can delete only your own posts")
	}

	if err := s.repo.DeletePost(ctx, req.GetPostId()); err != nil {
		if err == postgres.ErrPostNotFound {
			return nil, status.Error(codes.NotFound, "post not found")
		}
		return nil, status.Errorf(codes.Internal, "delete post: %v", err)
	}
	if err := s.likes.DeletePostData(ctx, req.GetPostId()); err != nil {
		return nil, status.Errorf(codes.Internal, "delete likes data: %v", err)
	}
	if err := s.mainCache.Invalidate(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "invalidate main page cache: %v", err)
	}

	return &blogv1.Empty{}, nil
}

func (s *BlogService) LikePost(ctx context.Context, req *blogv1.LikePostRequest) (*blogv1.Empty, error) {
	userID, err := userIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if req.GetPostId() == "" {
		return nil, status.Error(codes.InvalidArgument, "post_id is required")
	}
	if _, err := s.repo.GetPostByID(ctx, req.GetPostId()); err != nil {
		if err == postgres.ErrPostNotFound {
			return nil, status.Error(codes.NotFound, "post not found")
		}
		return nil, status.Errorf(codes.Internal, "get post: %v", err)
	}

	if err := s.likes.LikePost(ctx, req.GetPostId(), userID); err != nil {
		return nil, status.Errorf(codes.Internal, "like post: %v", err)
	}
	return &blogv1.Empty{}, nil
}

func (s *BlogService) UnlikePost(ctx context.Context, req *blogv1.UnlikePostRequest) (*blogv1.Empty, error) {
	userID, err := userIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if req.GetPostId() == "" {
		return nil, status.Error(codes.InvalidArgument, "post_id is required")
	}
	if _, err := s.repo.GetPostByID(ctx, req.GetPostId()); err != nil {
		if err == postgres.ErrPostNotFound {
			return nil, status.Error(codes.NotFound, "post not found")
		}
		return nil, status.Errorf(codes.Internal, "get post: %v", err)
	}

	if err := s.likes.UnlikePost(ctx, req.GetPostId(), userID); err != nil {
		return nil, status.Errorf(codes.Internal, "unlike post: %v", err)
	}
	return &blogv1.Empty{}, nil
}

func (s *BlogService) DebugString() string {
	return fmt.Sprintf("blog service: postgres + redis")
}
