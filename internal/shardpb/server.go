package shardpb

import (
	"context"
	"strings"
	"sync"

	"github.com/davisclrk/article_db/internal/shard"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	UnimplementedShardServiceServer
	node          *shard.Node
	mu            sync.Mutex
	replicaAddr   string
	replicaConn   *grpc.ClientConn
	replicaClient ShardServiceClient
}

func NewServer(node *shard.Node) *Server {
	return &Server{
		node:        node,
		replicaAddr: node.ReplicaAddr(),
	}
}

func (s *Server) Insert(ctx context.Context, req *InsertRequest) (*InsertResponse, error) {
	a := ArticleFromProto(req.GetArticle())
	if a == nil {
		return nil, status.Error(codes.InvalidArgument, "article is required")
	}
	if err := s.node.InsertArticle(a); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if s.node.IsPrimary && s.replicaAddr != "" {
		replicaClient, err := s.replicaClientFor()
		if err != nil {
			rollbackErr := s.node.DeleteArticle(a.ID)
			if rollbackErr != nil {
				return nil, status.Errorf(codes.Internal, "replica insert dial failed: %v; primary rollback failed: %v", err, rollbackErr)
			}
			return nil, status.Errorf(codes.Unavailable, "replica insert dial failed: %v", err)
		}
		_, err = replicaClient.Insert(ctx, &InsertRequest{Article: req.GetArticle()})
		if err != nil {
			rollbackErr := s.node.DeleteArticle(a.ID)
			if rollbackErr != nil {
				return nil, status.Errorf(codes.Internal, "replica insert failed: %v; primary rollback failed: %v", err, rollbackErr)
			}
			return nil, err
		}
	}
	return &InsertResponse{}, nil
}

func (s *Server) Get(ctx context.Context, req *GetRequest) (*GetResponse, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	a, err := s.node.GetArticle(req.GetId())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if a == nil {
		return nil, status.Error(codes.NotFound, "article not found")
	}
	return &GetResponse{Article: ArticleToProto(a)}, nil
}

func (s *Server) Delete(ctx context.Context, req *DeleteRequest) (*DeleteResponse, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	var existing *Article
	if s.node.IsPrimary && s.replicaAddr != "" {
		article, err := s.node.GetArticle(req.GetId())
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		if article == nil {
			return nil, status.Error(codes.NotFound, "article not found")
		}
		existing = ArticleToProto(article)
	}
	if err := s.node.DeleteArticle(req.GetId()); err != nil {
		if isNotFound(err) {
			return nil, status.Error(codes.NotFound, "article not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	if s.node.IsPrimary && s.replicaAddr != "" {
		replicaClient, err := s.replicaClientFor()
		if err != nil {
			if existing != nil {
				if insertErr := s.node.InsertArticle(ArticleFromProto(existing)); insertErr != nil {
					return nil, status.Errorf(codes.Internal, "replica delete dial failed: %v; primary rollback failed: %v", err, insertErr)
				}
			}
			return nil, status.Errorf(codes.Unavailable, "replica delete dial failed: %v", err)
		}
		_, err = replicaClient.Delete(ctx, req)
		if err != nil && status.Code(err) != codes.NotFound {
			if existing != nil {
				if insertErr := s.node.InsertArticle(ArticleFromProto(existing)); insertErr != nil {
					return nil, status.Errorf(codes.Internal, "replica delete failed: %v; primary rollback failed: %v", err, insertErr)
				}
			}
			return nil, err
		}
	}
	return &DeleteResponse{}, nil
}

func (s *Server) SearchSimilar(ctx context.Context, req *SearchSimilarRequest) (*SearchSimilarResponse, error) {
	hits, err := s.node.SearchSimilar(req.GetQuery(), int(req.GetK()))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := make([]*SearchHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, SearchHitToProto(h))
	}
	return &SearchSimilarResponse{Hits: out}, nil
}

func (s *Server) ListArticles(ctx context.Context, req *ListArticlesRequest) (*ListArticlesResponse, error) {
	articles, err := s.node.ListArticles()
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := make([]*Article, 0, len(articles))
	for _, a := range articles {
		out = append(out, ArticleToProto(a))
	}
	return &ListArticlesResponse{Articles: out}, nil
}

func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not found")
}

func (s *Server) replicaClientFor() (ShardServiceClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.replicaClient != nil {
		return s.replicaClient, nil
	}
	conn, err := grpc.NewClient(s.replicaAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	s.replicaConn = conn
	s.replicaClient = NewShardServiceClient(conn)
	return s.replicaClient, nil
}
