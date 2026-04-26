package coordinator

import (
	"context"
	"errors"

	"github.com/davisclrk/article_db/internal/models"
	"github.com/davisclrk/article_db/internal/shard"
	"github.com/davisclrk/article_db/internal/shardpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

var ErrNotFound = errors.New("article not found")

// return (nil, nil) if article does not exist
type Client interface {
	InsertArticle(ctx context.Context, a *models.Article) error
	GetArticle(ctx context.Context, id string) (*models.Article, error)
	DeleteArticle(ctx context.Context, id string) error
	SearchSimilar(ctx context.Context, query []float32, k int) ([]models.SearchResult, error)
	ListArticles(ctx context.Context) ([]*models.Article, error)
	Close() error
}

// backed by in-process *shard.Node
type LocalClient struct {
	node *shard.Node
}

func NewLocalClient(node *shard.Node) *LocalClient {
	return &LocalClient{node: node}
}

func (c *LocalClient) InsertArticle(_ context.Context, a *models.Article) error {
	return c.node.InsertArticle(a)
}

func (c *LocalClient) GetArticle(_ context.Context, id string) (*models.Article, error) {
	return c.node.GetArticle(id)
}

func (c *LocalClient) DeleteArticle(_ context.Context, id string) error {
	return c.node.DeleteArticle(id)
}

func (c *LocalClient) SearchSimilar(_ context.Context, query []float32, k int) ([]models.SearchResult, error) {
	return c.node.SearchSimilar(query, k)
}

func (c *LocalClient) ListArticles(_ context.Context) ([]*models.Article, error) {
	return c.node.ListArticles()
}

func (c *LocalClient) Close() error {
	return c.node.Close()
}

// backed by gRPC connection to shard
type RemoteClient struct {
	conn   *grpc.ClientConn
	client shardpb.ShardServiceClient
}

func NewRemoteClient(addr string) (*RemoteClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &RemoteClient{
		conn:   conn,
		client: shardpb.NewShardServiceClient(conn),
	}, nil
}

func (c *RemoteClient) InsertArticle(ctx context.Context, a *models.Article) error {
	_, err := c.client.Insert(ctx, &shardpb.InsertRequest{Article: shardpb.ArticleToProto(a)})
	return err
}

func (c *RemoteClient) GetArticle(ctx context.Context, id string) (*models.Article, error) {
	resp, err := c.client.Get(ctx, &shardpb.GetRequest{Id: id})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil
		}
		return nil, err
	}
	return shardpb.ArticleFromProto(resp.GetArticle()), nil
}

func (c *RemoteClient) DeleteArticle(ctx context.Context, id string) error {
	_, err := c.client.Delete(ctx, &shardpb.DeleteRequest{Id: id})
	if err != nil && status.Code(err) == codes.NotFound {
		return ErrNotFound
	}
	return err
}

func (c *RemoteClient) SearchSimilar(ctx context.Context, query []float32, k int) ([]models.SearchResult, error) {
	resp, err := c.client.SearchSimilar(ctx, &shardpb.SearchSimilarRequest{Query: query, K: int32(k)})
	if err != nil {
		return nil, err
	}
	hits := resp.GetHits()
	out := make([]models.SearchResult, 0, len(hits))
	for _, h := range hits {
		out = append(out, shardpb.SearchHitFromProto(h))
	}
	return out, nil
}

func (c *RemoteClient) ListArticles(ctx context.Context) ([]*models.Article, error) {
	resp, err := c.client.ListArticles(ctx, &shardpb.ListArticlesRequest{})
	if err != nil {
		return nil, err
	}
	articles := resp.GetArticles()
	out := make([]*models.Article, 0, len(articles))
	for _, a := range articles {
		out = append(out, shardpb.ArticleFromProto(a))
	}
	return out, nil
}

func (c *RemoteClient) Close() error {
	return c.conn.Close()
}
