// lingshu 是灵枢网关的唯一可执行程序。
//
//	lingshu [serve]                     启动服务（启动时自动执行数据库迁移）
//	lingshu migrate                     只执行数据库迁移
//	lingshu admin create -username NAME 创建管理员，密码从终端输入或标准输入读取
//	lingshu key create -username NAME -group GROUP -name KEYNAME
//	                                    创建 API key（前端做好之前的临时入口）
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"

	"github.com/aumi314/lingshu/internal/api"
	"github.com/aumi314/lingshu/internal/auth"
	"github.com/aumi314/lingshu/internal/config"
	"github.com/aumi314/lingshu/internal/relay"
	"github.com/aumi314/lingshu/internal/relay/usage"
	"github.com/aumi314/lingshu/internal/store"
	"github.com/aumi314/lingshu/web"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		return serve()
	case "migrate":
		return withStore(func(ctx context.Context, s *store.Store) error {
			if err := s.Migrate(ctx); err != nil {
				return err
			}
			fmt.Println("数据库迁移完成")
			return nil
		})
	case "admin":
		if len(args) == 0 || args[0] != "create" {
			return errors.New("用法: lingshu admin create -username NAME")
		}
		return adminCreate(args[1:])
	case "key":
		if len(args) == 0 || args[0] != "create" {
			return errors.New("用法: lingshu key create -username NAME -group GROUP -name KEYNAME")
		}
		return keyCreate(args[1:])
	default:
		return fmt.Errorf("未知命令 %q，可用命令: serve、migrate、admin create、key create", cmd)
	}
}

func withStore(fn func(context.Context, *store.Store) error) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	s, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer s.Close()
	return fn(ctx, s)
}

func serve() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	s, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.Migrate(ctx); err != nil {
		return err
	}
	// 上次进程里进行中的请求都已结束，它们冻结的额度一并释放
	if err := s.ResetReservations(ctx); err != nil {
		return err
	}
	// 检测在内存里跑，重启后不会继续：上次没跑完的标成失败
	if err := s.FailStaleProbeRuns(ctx); err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Pool.Ping(r.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})
	h := relay.NewHandler(s)
	mux.Handle("POST /v1/chat/completions", h.Serve(usage.Chat))
	mux.Handle("POST /v1/messages", h.Serve(usage.Messages))
	mux.Handle("POST /v1/responses", h.Serve(usage.Responses))
	mux.Handle("POST /v1/responses/compact", h.Serve(usage.Responses))
	mux.Handle("GET /v1/responses", h.ServeWebSocket())
	mux.Handle("POST /v1/systemone", h.Serve(usage.SystemOne))
	mux.HandleFunc("GET /v1/models", h.ServeModels)
	mux.HandleFunc("GET /v1/usage", h.ServeUsage)
	dist, _ := fs.Sub(web.Dist, "dist")
	apiServer := &api.Server{Store: s, Relay: h, Web: dist, PublicURL: cfg.PublicURL}
	apiServer.Register(mux)
	go apiServer.RunFX(ctx)

	// 先占住端口再报"已启动"，端口被占用时直接报错退出
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	slog.Info("灵枢已启动", "listen", cfg.Listen)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	slog.Info("正在关闭，等待进行中的请求结束")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func adminCreate(args []string) error {
	fs := flag.NewFlagSet("admin create", flag.ContinueOnError)
	username := fs.String("username", "", "管理员用户名")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *username == "" {
		return errors.New("请用 -username 指定用户名")
	}
	password, err := readPassword()
	if err != nil {
		return err
	}
	if len(password) < api.MinPasswordLen {
		return fmt.Errorf("密码至少 %d 位", api.MinPasswordLen)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return withStore(func(ctx context.Context, s *store.Store) error {
		if err := s.Migrate(ctx); err != nil {
			return err
		}
		id, err := s.CreateUser(ctx, *username, string(hash), "super_admin") // 初始化部署用，建的是超级管理员
		if err != nil {
			return err
		}
		fmt.Printf("超级管理员 %s 已创建（id=%d）\n", *username, id)
		return nil
	})
}

func keyCreate(args []string) error {
	fs := flag.NewFlagSet("key create", flag.ContinueOnError)
	username := fs.String("username", "", "key 所属用户")
	group := fs.String("group", "default", "key 使用的分组")
	name := fs.String("name", "", "key 名称")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *username == "" || *name == "" {
		return errors.New("请用 -username 和 -name 指定用户和 key 名称")
	}
	return withStore(func(ctx context.Context, s *store.Store) error {
		userID, err := s.UserIDByName(ctx, *username)
		if err != nil {
			return err
		}
		if userID == 0 {
			return fmt.Errorf("用户 %s 不存在", *username)
		}
		groupID, err := s.GroupIDByName(ctx, *group)
		if err != nil {
			return err
		}
		if groupID == 0 {
			return fmt.Errorf("分组 %s 不存在", *group)
		}
		key, hash, prefix := auth.NewAPIKey()
		if _, err := s.CreateAPIKey(ctx, userID, *name, hash, prefix, groupID, nil); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "key 已创建，只显示这一次，请马上保存：")
		fmt.Println(key)
		return nil
	})
}

// readPassword 在终端里不回显地输入两次；非终端（管道）时读一行。
func readPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", errors.New("没有从标准输入读到密码")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprint(os.Stderr, "密码: ")
	p1, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "再输一次: ")
	p2, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(p1) != string(p2) {
		return "", errors.New("两次输入的密码不一致")
	}
	return string(p1), nil
}
