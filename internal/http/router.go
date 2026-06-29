package http

import (
	"context"
	"feedsystem/internal/account"
	"feedsystem/internal/feed"
	"feedsystem/internal/message"
	jwtmiddleware "feedsystem/internal/middleware/jwt"
	"feedsystem/internal/middleware/rabbitmq"
	"feedsystem/internal/middleware/ratelimit"
	rediscache "feedsystem/internal/middleware/redis"
	"feedsystem/internal/social"
	"feedsystem/internal/video"
	"feedsystem/internal/worker"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func SetupRouter(db *gorm.DB, cache *rediscache.Client, rmq *rabbitmq.RabbitMQ) *gin.Engine {
	r := gin.Default()

	if err := r.SetTrustedProxies(nil); err != nil {
		log.Printf("SetTrustedProxies err: %v", err)
	}

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	r.Static("/static", "./.run/uploads")

	//限流中间件注册
	loginLimiter := ratelimit.Limit(cache, "account_login", 10, time.Minute, ratelimit.KeyByIP)
	registerLimiter := ratelimit.Limit(cache, "account_register", 5, time.Hour, ratelimit.KeyByIP)
	likeLimiter := ratelimit.Limit(cache, "like_action", 30, time.Minute, ratelimit.KeyByAccount)
	commentLimiter := ratelimit.Limit(cache, "comment_action", 20, time.Minute, ratelimit.KeyByAccount)
	socialLimiter := ratelimit.Limit(cache, "social_action", 30, time.Minute, ratelimit.KeyByAccount)

	//account
	accountRepository := account.NewAccountRepository(db)
	accountService := account.NewAccountService(accountRepository, cache)
	accountHandler := account.NewAccountHandler(accountService)

	accountGroup := r.Group("/account")
	{
		accountGroup.POST("/register", registerLimiter, accountHandler.CreateAccount)
		accountGroup.POST("/login", loginLimiter, accountHandler.Login)
		accountGroup.POST("/findByID", accountHandler.FindByID)
		accountGroup.POST("/findByUsername", accountHandler.FindByUsername)
		accountGroup.POST("/refresh", accountHandler.Refresh)
	}

	protectedAccountGroup := accountGroup.Group("")
	protectedAccountGroup.Use(jwtmiddleware.JWTAuth(accountRepository, cache))
	{
		protectedAccountGroup.POST("/logout", accountHandler.Logout)
		protectedAccountGroup.POST("/rename", accountHandler.Rename)
		protectedAccountGroup.POST("/uploadAvatar", accountHandler.UploadAvatar)
		protectedAccountGroup.POST("/updateProfile", accountHandler.UpdateProfile)
		protectedAccountGroup.POST("/changePassword", accountHandler.ChangePassword)
	}

	//video
	videoRepository := video.NewVideoRepository(db)
	videoService := video.NewVideoService(videoRepository, cache)
	videoHandler := video.NewVideoHandler(videoService)
	chunkUploadHandler := video.NewChunkUploadHandler(cache)

	videoGroup := r.Group("/video")
	{
		videoGroup.POST("/listByAuthorID", videoHandler.ListByAuthorID)
		videoGroup.POST("/getDetail", videoHandler.GetDetail)
	}

	protectedVideoGroup := videoGroup.Group("")
	protectedVideoGroup.Use(jwtmiddleware.JWTAuth(accountRepository, cache))
	{
		protectedVideoGroup.POST("/uploadVideo", videoHandler.UploadVideo)
		protectedVideoGroup.POST("/uploadCover", videoHandler.UploadCover)
		protectedVideoGroup.POST("/publish", videoHandler.PublishVideo)
		protectedVideoGroup.POST("/chunk/init", chunkUploadHandler.InitChunkUpload)
		protectedVideoGroup.POST("/chunk/upload", chunkUploadHandler.UploadChunk)
		protectedVideoGroup.POST("/chunk/status", chunkUploadHandler.ChunkStatus)
		protectedVideoGroup.POST("/chunk/complete", chunkUploadHandler.CompleteChunkUpload)
	}

	//like
	likeRepository := video.NewLikeRepository(db)
	likeMQ, err := rabbitmq.NewLikeMQ(rmq)
	if err != nil {
		log.Printf("LikeMQ init failed (mq disabled): %v", err)
		//当 `rmq == nil` 时，`NewLikeMQ` 返回错误，路由仍继续创建。这就是 API 的降级路径
		likeMQ = nil
	}
	popularityMQ, err := rabbitmq.NewPopularityMQ(rmq)
	if err != nil {
		log.Printf("PopularityMQ init failed (mq disabled): %v", err)
		popularityMQ = nil
	}

	likeService := video.NewLikeService(likeRepository, videoRepository, cache, likeMQ, popularityMQ)
	likeHandler := video.NewLikeHandler(likeService)

	likeGroup := r.Group("/like")
	protectedLikeGroup := likeGroup.Group("")
	protectedLikeGroup.Use(jwtmiddleware.JWTAuth(accountRepository, cache))
	{
		protectedLikeGroup.POST("/like", likeLimiter, likeHandler.Like)
		protectedLikeGroup.POST("/unlike", likeLimiter, likeHandler.Unlike)
		protectedLikeGroup.POST("/isLiked", likeHandler.IsLiked)
		protectedLikeGroup.POST("/listMyLikedVideos", likeHandler.ListMyLikedVideos)
	}

	//comment
	commentRepository := video.NewCommentRepository(db)
	commentMQ, err := rabbitmq.NewCommentMQ(rmq)
	if err != nil {
		log.Printf("CommentMQ init failed (mq disabled): %v", err)
		commentMQ = nil
	}
	commentService := video.NewCommentService(commentRepository, videoRepository, cache, commentMQ, popularityMQ)
	commentHandler := video.NewCommentHandler(commentService, accountService)

	commentGroup := r.Group("/comment")
	{
		commentGroup.POST("/listAll", commentHandler.GetAllComments)
	}

	protectedCommentGroup := commentGroup.Group("")
	protectedCommentGroup.Use(jwtmiddleware.JWTAuth(accountRepository, cache))
	{
		protectedCommentGroup.POST("/publish", commentLimiter, commentHandler.PublishComment)
		protectedCommentGroup.POST("/delete", commentLimiter, commentHandler.DeleteComment)
	}

	//social
	socialRepository := social.NewSocialRepository(db)
	socialMQ, err := rabbitmq.NewSocialMQ(rmq)
	if err != nil {
		log.Printf("SocialMQ init failed (mq disabled): %v", err)
		socialMQ = nil
	}
	socialService := social.NewSocialService(socialRepository, accountRepository, socialMQ, cache)
	socialHandler := social.NewSocialHandler(socialService)

	socialGroup := r.Group("/social")
	protectedSocialGroup := socialGroup.Group("")
	protectedSocialGroup.Use(jwtmiddleware.JWTAuth(accountRepository, cache))
	{
		protectedSocialGroup.POST("/follow", socialLimiter, socialHandler.Follow)
		protectedSocialGroup.POST("/unfollow", socialLimiter, socialHandler.Unfollow)
		protectedSocialGroup.POST("/getAllFollowers", socialHandler.GetAllFollowers)
		protectedSocialGroup.POST("/getAllVloggers", socialHandler.GetAllVloggers)
		protectedSocialGroup.POST("/getCounts", socialHandler.GetCounts)
	}

	timelineMQ, err := rabbitmq.NewTimelineMQ(rmq)
	if err != nil {
		log.Printf("TimelineMQ init failed (mq disabled): %v", err)
		timelineMQ = nil
	}
	//由于accountGroup的getProfile需要用到videoRepo和socialRepo，所以放在了这后面
	accountGroup.POST("/getProfile", func(c *gin.Context) {
		var req account.GetProfileRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.AccountID == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "account_id is required"})
			return
		}

		acc, err := accountService.FindByID(c.Request.Context(), req.AccountID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		videoCount, _ := videoRepository.CountByAuthor(c.Request.Context(), req.AccountID)
		totalLikes, _ := videoRepository.TotalLikesByAuthor(c.Request.Context(), req.AccountID)
		followerCount, _ := socialRepository.CountFollowers(c.Request.Context(), req.AccountID)
		vloggerCount, _ := socialRepository.CountVloggers(c.Request.Context(), req.AccountID)

		c.JSON(http.StatusOK, account.GetProfileResponse{
			Account: account.FindByIDResponse{
				ID:        acc.ID,
				Username:  acc.Username,
				AvatarURL: acc.AvatarURL,
				Bio:       acc.Bio,
			},
			VideoCount:    videoCount,
			TotalLikes:    totalLikes,
			FollowerCount: followerCount,
			VloggerCount:  vloggerCount,
		})
	})

	//worker
	worker.StartOutboxPoller(db, timelineMQ)
	worker.StartConsumer(timelineMQ, "video.timeline.update.queue", cache, rmq)

	// feed
	feedRepository := feed.NewFeedRepository(db)
	feedService := feed.NewFeedService(feedRepository, likeRepository, cache)
	feedHandler := feed.NewFeedHandler(feedService)

	feedGroup := r.Group("/feed")
	//未登录用户也能看点赞排序 Feed。
	//登录用户可以额外看到 is_liked=true/false
	feedGroup.Use(jwtmiddleware.SoftJWTAuth(accountRepository, cache))
	{
		feedGroup.POST("/listLatest", feedHandler.ListLatest)
		feedGroup.POST("/listLikesCount", feedHandler.ListLikesCount)
		feedGroup.POST("/listByTag", feedHandler.ListByTag)
		feedGroup.POST("/listByPopularity", feedHandler.ListByPopularity)
	}

	protectedFeedGroup := feedGroup.Group("")
	protectedFeedGroup.Use(jwtmiddleware.JWTAuth(accountRepository, cache))
	{
		protectedFeedGroup.POST("/listByFollowing", feedHandler.ListByFollowing)
	}

	//message
	messageRepo := message.NewRepository(db)
	messageService := message.NewService(messageRepo)
	messageHandler := message.NewHandler(messageService)

	messageGroup := r.Group("/message")
	protectedMessageGroup := messageGroup.Group("")
	protectedMessageGroup.Use(jwtmiddleware.JWTAuth(accountRepository, cache))
	{
		protectedMessageGroup.POST("/send", messageHandler.Send)
		protectedMessageGroup.POST("/list", messageHandler.List)
	}

	/* SSE notification：API 进程内启动，不放到 cmd/worker。
	原因是：SSEHub 只存在 API 进程内存里。NotificationWorker 想实时 Push，就必须拿到这个 API 进程里的 sseHub*/
	if rmq != nil {
		if notifCh, err := rmq.NewChannel(); err == nil {
			//1. 声明 notification.like 队列，绑定 like.events 的 like.like
			if err := rabbitmq.DeclareTopic(notifCh, "like.events", "notification.like", "like.like"); err != nil {
				log.Printf("notification like topic init failed: %v", err)
			}
			//2. 声明 notification.comment 队列，绑定 comment.events 的 comment.publish
			if err := rabbitmq.DeclareTopic(notifCh, "comment.events", "notification.comment", "comment.publish"); err != nil {
				log.Printf("notification comment topic init failed: %v", err)
			}
			//3. 声明 notification.social 队列，绑定 social.events 的 social.follow
			if err := rabbitmq.DeclareTopic(notifCh, "social.events", "notification.social", "social.follow"); err != nil {
				log.Printf("notification social topic init failed: %v", err)
			}
			_ = notifCh.Close()
		} else {
			log.Printf("notification topic init channel failed: %v", err)
		}
	}
	//申明路由
	sseHub := worker.NewSSEHub(db)
	notifGroup := r.Group("/notification")
	notifGroup.Use(sseHub.SSERequireAuth())
	sseHub.RegisterRoutes(r, notifGroup)
	//4. 启动 3 个 NotificationWorker，各消费一个独立队列
	go func() {
		if rmq != nil {
			hub := sseHub
			ctx := context.Background()

			for _, q := range []string{"notification.like", "notification.comment", "notification.social"} {
				go func(queue string) {
					for {
						ch, err := rmq.NewChannel()
						if err != nil {
							log.Printf("notification-%s: 创建 Channel 失败: %v, 5秒后重试", queue, err)
							time.Sleep(5 * time.Second)
							continue
						}

						w := worker.NewNotificationWorker(ch, db, queue, hub)
						if err := w.Run(ctx); err != nil {
							log.Printf("notification-%s: %v, 5秒后重连...", queue, err)
						}

						_ = ch.Close()
						time.Sleep(5 * time.Second)
					}
				}(q)
			}
		} else {
			log.Printf("Notification SSE disabled (MQ not available)")
		}
	}()

	return r
}
