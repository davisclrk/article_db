package shardpb

import (
	"github.com/davisclrk/article_db/internal/models"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func ArticleToProto(a *models.Article) *Article {
	if a == nil {
		return nil
	}
	return &Article{
		Id:        a.ID,
		Url:       a.URL,
		Headline:  a.Headline,
		Summary:   a.Summary,
		Content:   a.Content,
		Vector:    a.Vector,
		ShardId:   int32(a.ShardID),
		CreatedAt: timestamppb.New(a.CreatedAt),
	}
}

func ArticleFromProto(p *Article) *models.Article {
	if p == nil {
		return nil
	}
	a := &models.Article{
		ID:       p.GetId(),
		URL:      p.GetUrl(),
		Headline: p.GetHeadline(),
		Summary:  p.GetSummary(),
		Content:  p.GetContent(),
		Vector:   p.GetVector(),
		ShardID:  int(p.GetShardId()),
	}
	if ts := p.GetCreatedAt(); ts != nil {
		a.CreatedAt = ts.AsTime()
	}
	return a
}

func SearchHitToProto(r models.SearchResult) *SearchHit {
	a := r.Article
	a.Vector = nil
	return &SearchHit{
		Article: ArticleToProto(&a),
		Score:   r.Score,
	}
}

func SearchHitFromProto(h *SearchHit) models.SearchResult {
	if h == nil {
		return models.SearchResult{}
	}
	return models.SearchResult{
		Article: *ArticleFromProto(h.GetArticle()),
		Score:   h.GetScore(),
	}
}
