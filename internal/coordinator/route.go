package coordinator

import (
	"context"
	"errors"
	"fmt"

	"github.com/davisclrk/article_db/internal/models"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type shardRoute struct {
	shardID int
	primary *routeEndpoint
	replica *routeEndpoint
}

type routeEndpoint struct {
	addr   string
	client Client
}

func newLocalShardRoute(shardID int, client Client) *shardRoute {
	return &shardRoute{
		shardID: shardID,
		primary: &routeEndpoint{client: client},
	}
}

func newRemoteShardRoute(shardID int, cfg ReplicaSetConfig) *shardRoute {
	return &shardRoute{
		shardID: shardID,
		primary: &routeEndpoint{addr: cfg.PrimaryAddr, client: cfg.Primary},
		replica: &routeEndpoint{addr: cfg.ReplicaAddr, client: cfg.Replica},
	}
}

func (r *shardRoute) InsertArticle(ctx context.Context, article *models.Article) error {
	return r.primary.client.InsertArticle(ctx, article)
}

func (r *shardRoute) GetArticle(ctx context.Context, id string) (*models.Article, error) {
	return readWithFailover(r, func(client Client) (*models.Article, error) {
		return client.GetArticle(ctx, id)
	})
}

func (r *shardRoute) DeleteArticle(ctx context.Context, id string) error {
	return r.primary.client.DeleteArticle(ctx, id)
}

func (r *shardRoute) SearchSimilar(ctx context.Context, query []float32, k int) ([]models.SearchResult, error) {
	return readWithFailover(r, func(client Client) ([]models.SearchResult, error) {
		return client.SearchSimilar(ctx, query, k)
	})
}

func (r *shardRoute) ListArticles(ctx context.Context) ([]*models.Article, error) {
	return readWithFailover(r, func(client Client) ([]*models.Article, error) {
		return client.ListArticles(ctx)
	})
}

func (r *shardRoute) Close() error {
	var firstErr error
	if r.primary != nil {
		if err := r.primary.client.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if r.replica != nil {
		if err := r.replica.client.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (r *shardRoute) readEndpoints() (*routeEndpoint, *routeEndpoint) {
	if r.replica == nil {
		return r.primary, nil
	}
	return r.primary, r.replica
}

func readWithFailover[T any](route *shardRoute, op func(Client) (T, error)) (T, error) {
	var zero T

	first, second := route.readEndpoints()
	if first == nil {
		return zero, fmt.Errorf("shard %d has no readable endpoint", route.shardID)
	}

	value, err := op(first.client)
	if err == nil {
		return value, nil
	}
	if second == nil || !isFailoverError(err) {
		return zero, err
	}

	value, secondErr := op(second.client)
	if secondErr != nil {
		return zero, secondErr
	}
	return value, nil
}

func isFailoverError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	code := status.Code(err)
	return code == codes.Unavailable || code == codes.DeadlineExceeded
}
