package common

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

// CompressAndEncryptRaw 压缩并加密数据（返回二进制，不做 Base64 编码）
func CompressAndEncryptRaw(data []byte, salt string) ([]byte, error) {
	// 压缩数据
	var compressedBuf bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressedBuf)

	_, err := gzipWriter.Write(data)
	if err != nil {
		return nil, fmt.Errorf("压缩数据失败: %v", err)
	}

	if err := gzipWriter.Close(); err != nil {
		return nil, fmt.Errorf("关闭gzip写入器失败: %v", err)
	}

	compressedData := compressedBuf.Bytes()

	// 创建AES加密器
	block, err := aes.NewCipher([]byte(salt))
	if err != nil {
		return nil, fmt.Errorf("创建AES加密器失败: %v", err)
	}

	// 创建GCM模式加密器
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("创建GCM失败: %v", err)
	}

	// 创建12字节的nonce
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("生成nonce失败: %v", err)
	}

	// 加密数据
	ciphertext := aesgcm.Seal(nil, nonce, compressedData, nil)

	// 返回 nonce + 密文（二进制）
	return append(nonce, ciphertext...), nil
}
