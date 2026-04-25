package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type Post struct {
	ID        uint64    `gorm:"primaryKey"`
	AuthorID  string    `gorm:"type:text;not null;index"`
	Nickname  string    `gorm:"type:text;not null"`
	AvatarURL string    `gorm:"type:text;not null"`
	Body      string    `gorm:"type:text;not null"`
	CreatedAt time.Time `gorm:"not null;index"`
	UpdatedAt time.Time `gorm:"not null"`
}

type PostRepository struct {
	db *gorm.DB
}

func New(ctx context.Context, dsn string) (*PostRepository, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	if err := db.WithContext(ctx).AutoMigrate(&Post{}); err != nil {
		return nil, fmt.Errorf("migrate postgres: %w", err)
	}

	return &PostRepository{db: db}, nil
}

func (r *PostRepository) ListPosts(ctx context.Context, limit, offset int) ([]Post, error) {
	var posts []Post
	if err := r.db.WithContext(ctx).
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&posts).Error; err != nil {
		return nil, fmt.Errorf("list posts: %w", err)
	}
	return posts, nil
}

func (r *PostRepository) CreatePost(ctx context.Context, post *Post) error {
	if err := r.db.WithContext(ctx).Create(post).Error; err != nil {
		return fmt.Errorf("create post: %w", err)
	}
	return nil
}

func (r *PostRepository) GetPostByID(ctx context.Context, id string) (*Post, error) {
	parsedID, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse post id: %w", err)
	}

	var post Post
	if err := r.db.WithContext(ctx).First(&post, parsedID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrPostNotFound
		}
		return nil, fmt.Errorf("get post: %w", err)
	}

	return &post, nil
}

func (r *PostRepository) UpdatePostBody(ctx context.Context, id, body string) (*Post, error) {
	post, err := r.GetPostByID(ctx, id)
	if err != nil {
		return nil, err
	}

	post.Body = body
	if err := r.db.WithContext(ctx).Save(post).Error; err != nil {
		return nil, fmt.Errorf("update post: %w", err)
	}

	return post, nil
}

func (r *PostRepository) DeletePost(ctx context.Context, id string) error {
	parsedID, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return fmt.Errorf("parse post id: %w", err)
	}

	result := r.db.WithContext(ctx).Delete(&Post{}, parsedID)
	if result.Error != nil {
		return fmt.Errorf("delete post: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrPostNotFound
	}

	return nil
}

var ErrPostNotFound = errors.New("post not found")
