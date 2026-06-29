package worker

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"feedsystem/internal/auth"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type SSEHub struct {
	//读写锁
	//多个 读 操作可以同时进行
	//多个线程同时读是可以的，但是同时写或者一读一写是不行的
	mu sync.RWMutex
	//clients[1] = [ch1, ch2, ch3]----------说明用户 1 当前有 3 个页面正在连 SSE
	clients map[uint][]chan *Notification
	db      *gorm.DB
}

func NewSSEHub(db *gorm.DB) *SSEHub {
	return &SSEHub{clients: make(map[uint][]chan *Notification), db: db}
}

// 给某个用户当前所有在线连接推送通知
func (h *SSEHub) Push(userID uint, n *Notification) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	chs, ok := h.clients[userID]
	if !ok {
		return
	}

	for _, ch := range chs {
		//尝试把通知放进 channel。
		//如果 channel 没满，就放进去。
		//如果 channel 满了，不要卡住，直接跳过，防止卡住
		select {
		case ch <- n:
		default:
		}
	}
}

func (h *SSEHub) Subscribe(userID uint) chan *Notification {
	ch := make(chan *Notification, 20)

	h.mu.Lock()
	h.clients[userID] = append(h.clients[userID], ch)
	h.mu.Unlock()

	return ch
}

func (h *SSEHub) Unsubscribe(userID uint, ch chan *Notification) {
	h.mu.Lock()
	defer h.mu.Unlock()

	chs := h.clients[userID]
	for i, c := range chs {
		if c == ch {
			//保留 i 前面的元素,跳过第 i 个元素,再拼上 i 后面的元素
			chs = append(chs[:i], chs[i+1:]...)
			if len(chs) == 0 {
				delete(h.clients, userID)
			} else {
				h.clients[userID] = chs
			}
			close(c)
			return
		}
	}
}

func sseAccountID(c *gin.Context) (uint, bool) {
	accountID, ok := c.Get("accountID")
	if !ok {
		return 0, false
	}

	userID, ok := accountID.(uint)
	return userID, ok && userID != 0
}

func (h *SSEHub) SSERequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.Query("token")
		if token == "" {
			token = c.GetHeader("Authorization")
			if len(token) > 7 && token[:7] == "Bearer " {
				token = token[7:]
			}
		}

		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}

		claims, err := auth.ParseToken(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}

		c.Set("accountID", claims.AccountID)
		c.Next()
	}
}

func (h *SSEHub) SSEHandler(c *gin.Context) {
	userID, ok := sseAccountID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid account"})
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.WriteHeader(http.StatusOK)

	ch := h.Subscribe(userID)
	defer h.Unsubscribe(userID, ch)

	ctx := c.Request.Context()
	flusher, _ := c.Writer.(http.Flusher)

	for {
		select {
		case <-ctx.Done():
			return

		case n, ok := <-ch:
			if !ok {
				return
			}

			b, _ := json.Marshal(n)
			//数据不一定立刻到浏览器，可能先存在缓冲区里
			//SSE 的格式不是随便写的，标准 SSE 数据格式是：data: 内容 \n\n
			fmt.Fprintf(c.Writer, "data: %s\n\n", b)

			if flusher != nil {
				//马上把数据刷出去，立刻发给前端，避免放在缓冲区
				flusher.Flush()
			}

		case <-time.After(30 * time.Second):
			//这个东西叫 心跳包
			//冒号在sse中是注释的意思，前端不会当成业务消息处理
			//告诉浏览器、Nginx、网关、代理：这个连接还活着，不要因为太久没数据就把它断掉。
			fmt.Fprintf(c.Writer, ": keepalive\n\n")

			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func (h *SSEHub) ListHandler(c *gin.Context) {
	userID, ok := sseAccountID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid account"})
		return
	}

	var notifications []Notification
	if err := h.db.WithContext(c.Request.Context()).
		Where("recipient_id = ?", userID).
		Order("created_at desc").
		Limit(50).
		Find(&notifications).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if notifications == nil {
		notifications = []Notification{}
	}

	c.JSON(http.StatusOK, gin.H{"notifications": notifications})
}

func (h *SSEHub) MarkReadHandler(c *gin.Context) {
	userID, ok := sseAccountID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid account"})
		return
	}

	var req struct {
		ID *uint `json:"id"`
	}

	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var err error
	if req.ID != nil {
		err = h.db.WithContext(c.Request.Context()).
			Model(&Notification{}).
			Where("id = ? AND recipient_id = ?", *req.ID, userID).
			Update("is_read", true).Error
	} else {
		err = h.db.WithContext(c.Request.Context()).
			Model(&Notification{}).
			Where("recipient_id = ?", userID).
			Update("is_read", true).Error
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

func (h *SSEHub) UnreadCountHandler(c *gin.Context) {
	userID, ok := sseAccountID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid account"})
		return
	}

	var count int64
	if err := h.db.WithContext(c.Request.Context()).
		Model(&Notification{}).
		Where("recipient_id = ? AND is_read = ?", userID, false).
		Count(&count).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"count": count})
}

func (h *SSEHub) RegisterRoutes(r *gin.Engine, group *gin.RouterGroup) {
	//实时 SSE 通知流
	group.GET("/stream", h.SSEHandler)
	//从数据库获取最近 50 条通知
	group.POST("/list", h.ListHandler)
	//标记通知已读
	group.POST("/markRead", h.MarkReadHandler)
	//获取未读数量
	group.POST("/unreadCount", h.UnreadCountHandler)
}

var _ NotificationHub = (*SSEHub)(nil)
