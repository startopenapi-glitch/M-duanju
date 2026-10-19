package handler

// 私钥文件检查（供诊断接口使用）

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
)

func checkPrivateKey(path string) (bool, string) {
	if strings.TrimSpace(path) == "" {
		return false, "未配置 WXPAY_KEY_PATH"
	}
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, "文件不存在——需从微信支付商户平台下载 apiclient_key.pem 上传到该路径"
		}
		return false, "无法访问：" + err.Error()
	}
	if fi.IsDir() {
		return false, "该路径是目录，不是文件"
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return false, "读取失败：" + err.Error()
	}
	blk, _ := pem.Decode(raw)
	if blk == nil {
		return false, "不是合法 PEM 文件（内容应以 -----BEGIN PRIVATE KEY----- 开头）"
	}
	if k, err := x509.ParsePKCS1PrivateKey(blk.Bytes); err == nil {
		return true, fmt.Sprintf("已加载（PKCS#1，%d 位）", k.N.BitLen())
	}
	if k8, err := x509.ParsePKCS8PrivateKey(blk.Bytes); err == nil {
		if rk, ok := k8.(*rsa.PrivateKey); ok {
			return true, fmt.Sprintf("已加载（PKCS#8，%d 位）", rk.N.BitLen())
		}
		return false, "PEM 内容不是 RSA 私钥"
	}
	return false, "私钥格式无法解析（需 PKCS#1 或 PKCS#8 RSA 私钥）"
}
