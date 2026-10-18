// lingshu 是灵枢网关的唯一可执行程序。
//
//	lingshu [serve]                     启动服务（启动时自动执行数据库迁移）
//	lingshu migrate                     只执行数据库迁移
//	lingshu admin create -username NAME 创建管理员，密码从终端输入或标准输入读取
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"

	"github.com/aumi314/lingshu/internal/config"
	"github.com/aumi314/lingshu/internal/store"
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
	default:
		return fmt.Errorf("未知命令 %q，可用命令: serve、migrate、admin create", cmd)
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

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Pool.Ping(r.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})

	srv := &http.Server{Addr: cfg.Listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
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
	if len(password) < 8 {
		return errors.New("密码至少 8 位")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return withStore(func(ctx context.Context, s *store.Store) error {
		if err := s.Migrate(ctx); err != nil {
			return err
		}
		id, err := s.CreateUser(ctx, *username, string(hash), "admin")
		if err != nil {
			return err
		}
		fmt.Printf("管理员 %s 已创建（id=%d）\n", *username, id)
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
