package feed

import (
	"feedsystem/internal/apierror"
	"feedsystem/internal/middleware/jwt"
	"time"

	"github.com/gin-gonic/gin"
)

type FeedHandler struct {
	service *FeedService
}

func NewFeedHandler(service *FeedService) *FeedHandler {
	return &FeedHandler{service: service}
}

func (f *FeedHandler) ListLatest(c *gin.Context) {
	var req ListLatestRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHTTPStatus(err), gin.H{"error": err.Error()})
		return
	}

	if req.Limit <= 0 || req.Limit > 50 {
		req.Limit = 10
	}

	var latestTime time.Time
	if req.LatestTime > 0 {
		//int64 -> time.Time
		latestTime = time.UnixMilli(req.LatestTime)
	}

	viewerAccountID, err := jwt.GetAccountID(c)
	if err != nil {
		viewerAccountID = 0
	}

	resp, err := f.service.ListLatest(c.Request.Context(), req.Limit, latestTime, viewerAccountID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	resp.VideoList = nonNilFeedVideoItems(resp.VideoList)
	c.JSON(200, resp)
}

// 按照关注列表查询视频
func (f *FeedHandler) ListByFollowing(c *gin.Context) {
	var req ListByFollowingRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHTTPStatus(err), gin.H{"error": err.Error()})
		return
	}

	if req.Limit <= 0 || req.Limit > 50 {
		req.Limit = 10
	}

	var latestTime time.Time
	if req.LatestTime > 0 {
		latestTime = time.UnixMilli(req.LatestTime)
	}

	viewerAccountID, err := jwt.GetAccountID(c)
	if err != nil {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}

	resp, err := f.service.ListByFollowing(c.Request.Context(), req.Limit, latestTime, viewerAccountID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	resp.VideoList = nonNilFeedVideoItems(resp.VideoList)
	c.JSON(200, resp)
}

func nonNilFeedVideoItems(items []FeedVideoItem) []FeedVideoItem {
	if items == nil {
		return []FeedVideoItem{}
	}
	return items
}

// 根据点赞数排序推荐
func (f *FeedHandler) ListLikesCount(c *gin.Context) {
	var req ListLikesCountRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHTTPStatus(err), gin.H{"error": err.Error()})
		return
	}

	if req.Limit <= 0 || req.Limit > 50 {
		req.Limit = 10
	}

	var cursor *LikesCountCursor
	if req.LikesCountBefore != nil || req.IDBefore != nil {
		if req.LikesCountBefore == nil || req.IDBefore == nil {
			c.JSON(400, gin.H{"error": "likes_count_before and id_before must be provided together"})
			return
		}

		likesCountBefore := *req.LikesCountBefore
		idBefore := *req.IDBefore

		if likesCountBefore < 0 {
			c.JSON(400, gin.H{"error": "invalid cursor: likes_count_before must be >= 0"})
			return
		}

		if idBefore == 0 {
			if likesCountBefore != 0 {
				c.JSON(400, gin.H{"error": "invalid cursor: id_before must be > 0"})
				return
			}
		} else {
			cursor = &LikesCountCursor{
				LikesCount: likesCountBefore,
				ID:         idBefore,
			}
		}
	}

	viewerAccountID, err := jwt.GetAccountID(c)
	if err != nil {
		viewerAccountID = 0
	}

	resp, err := f.service.ListLikesCount(c.Request.Context(), req.Limit, cursor, viewerAccountID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	resp.VideoList = nonNilFeedVideoItems(resp.VideoList)
	c.JSON(200, resp)
}

func (f *FeedHandler) ListByTag(c *gin.Context) {
	var req ListByTagRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHTTPStatus(err), gin.H{"error": err.Error()})
		return
	}

	if req.TagName == "" {
		c.JSON(400, gin.H{"error": "tag_name is required"})
		return
	}

	if req.Limit <= 0 || req.Limit > 50 {
		req.Limit = 10
	}

	viewerAccountID, err := jwt.GetAccountID(c)
	if err != nil {
		viewerAccountID = 0
	}

	resp, err := f.service.ListByTag(c.Request.Context(), req.TagName, req.Limit, viewerAccountID)
	if err != nil {
		c.JSON(apierror.ClassifyHTTPStatus(err), gin.H{"error": err.Error()})
		return
	}

	resp.VideoList = nonNilFeedVideoItems(resp.VideoList)
	c.JSON(200, resp)
}

func (f *FeedHandler) ListByPopularity(c *gin.Context) {
	var req ListByPopularityRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(apierror.ClassifyHTTPStatus(err), gin.H{"error": err.Error()})
		return
	}

	if req.Limit <= 0 || req.Limit > 50 {
		req.Limit = 10
	}

	var cursor *PopularityCursor
	//判断是否传了至少一个游标，如果一个都没传，说明是第一页，游标都为nil
	if req.PopularityBefore != nil || req.LatestTime != nil || req.IDBefore != nil {
		//如果至少传了一个，那么必须三个都一起传
		if req.PopularityBefore == nil || req.LatestTime == nil || req.IDBefore == nil {
			c.JSON(400, gin.H{"error": "popularity_before, latest_time and id_before must be provided together"})
			return
		}

		popularityBefore := *req.PopularityBefore
		latestTime := *req.LatestTime
		idBefore := *req.IDBefore

		if popularityBefore < 0 {
			c.JSON(400, gin.H{"error": "invalid cursor: popularity_before must be >= 0"})
			return
		}
		if latestTime <= 0 {
			c.JSON(400, gin.H{"error": "invalid cursor: latest_time must be > 0"})
			return
		}
		if idBefore == 0 {
			c.JSON(400, gin.H{"error": "invalid cursor: id_before must be > 0"})
			return
		}

		cursor = &PopularityCursor{
			Popularity: popularityBefore,
			CreateTime: time.UnixMilli(latestTime),
			ID:         idBefore,
		}
	}

	viewerAccountID, err := jwt.GetAccountID(c)
	if err != nil {
		viewerAccountID = 0
	}

	resp, err := f.service.ListByPopularity(c.Request.Context(), req.Limit, cursor, viewerAccountID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	resp.VideoList = nonNilFeedVideoItems(resp.VideoList)
	c.JSON(200, resp)
}
