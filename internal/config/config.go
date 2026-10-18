// Package config 从环境变量读取配置。
package config

import (
	"errors"
	"os"
)

type Config struct {
	Listen      string // 监听地址
	DatabaseURL string
}

func Load() (Config, error) {
	c := Config{
		Listen:      getenv("LINGSHU_LISTEN", "127.0.0.1:8300"),
		DatabaseURL: os.Getenv("LINGSHU_DATABASE_URL"),
	}
	if c.DatabaseURL == "" {
		return c, errors.New("没有设置 LINGSHU_DATABASE_URL，例如 postgres://lingshu:密码@127.0.0.1:5432/lingshu")
	}
	return c, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
