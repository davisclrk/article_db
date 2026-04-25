package shardpb

import (
	"context"
	"strings"

	"github.com/davisclrk/article_db/internal/shard"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	UnimplementedShardServiceServer
	node *shard.Node
}

func NewServer(node *shard.Node) *Server {
	return &Server{node: node}
}

func (s *Server) Insert(ctx context.Context, req *InsertRequest) (*InsertResponse, error) {
	a := ArticleFromProto(req.GetArticle())
	if a == nil {
		return nil, status.Error(codes.InvalidArgument, "article is required")
	}
	if err := s.node.InsertArticle(a); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
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
	if err := s.node.DeleteArticle(req.GetId()); err != nil {
		if isNotFound(err) {
			return nil, status.Error(codes.NotFound, "article not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
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
