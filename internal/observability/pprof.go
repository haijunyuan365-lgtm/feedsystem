package observability

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"time"
)

type PprofServer struct {
	//表示这个 pprof 服务叫什么
	name string
	//真正的 HTTP 服务对象
	server *http.Server
	//服务退出时，给 pprof 服务最多 3 秒优雅关闭时间
	shutdownTimeout time.Duration
}

// 创建一个专门处理 pprof 请求的路由器
func NewPprofMux() *http.ServeMux {
	mux := http.NewServeMux()
	//pprof 首页
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	//接口返回程序启动时的命令行参数
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	//CPU Profile。它会采样一段时间内 CPU 都花在哪些函数上
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	//符号解析接口,主要是给 go tool pprof 辅助使用的，帮助把地址解析成函数名
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	//trace 更细，它能看：goroutine 怎么调度,网络阻塞,系统调用,GC,定时器,channel 阻塞
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	return mux
}

func NewPprofServer(name string, enabled bool, addr string) (*PprofServer, error) {
	if !enabled || addr == "" {
		return nil, nil
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to start %s pprof server on %s: %w", name, addr, err)
	}
	pprofServer := &PprofServer{
		name:            name,
		shutdownTimeout: 3 * time.Second,
	}
	pprofServer.server = &http.Server{
		Addr:              addr,
		Handler:           NewPprofMux(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("%s pprof listening on %s", name, addr)
		if err := pprofServer.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("%s pprof server error: %v", name, err)
		}
	}()
	return pprofServer, nil
}

func (s *PprofServer) Close() error {
	if s == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()
	if err := Shutdown(shutdownCtx, s.server); err != nil {
		log.Printf("Failed to shutdown %s pprof server: %v", s.name, err)
		return err
	}
	return nil
}

func Shutdown(ctx context.Context, srv *http.Server) error {
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}
