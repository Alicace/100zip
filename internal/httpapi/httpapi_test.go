package httpapi

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/100zip/100zip/internal/apperr"
)

// 分卷只下了一部分时，引擎只会报 EOF/无法打开（实测 7-Zip 26.03），
// 必须结合「文件是分卷命名」这一上下文还原成「缺少分卷」。
func TestRefineVolumeError(t *testing.T) {
	dir := t.TempDir()

	vol := filepath.Join(dir, "movie.7z.001")
	if err := os.WriteFile(vol, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	corrupt := apperr.New(apperr.CodeArchiveCorrupt, "压缩包已损坏或不完整", "尝试完整性测试或重新下载")
	got := refineVolumeError(corrupt, vol)
	ae, ok := got.(*apperr.Error)
	if !ok || ae.Code != apperr.CodeMissingVolume {
		t.Fatalf("分卷场景应升级为 MISSING_VOLUME，得到 %#v", got)
	}
	if ae.Hint == "" || ae.Message == corrupt.Message {
		t.Fatalf("升级后的提示信息不完整：%+v", ae)
	}

	// ENGINE_FAILED 可能是超时/引擎启动失败，不属于「打不开」，不能误报为缺卷
	engineFail := apperr.New(apperr.CodeEngineFailed, "引擎执行失败", "查看诊断信息")
	if got := refineVolumeError(engineFail, vol); got != error(engineFail) {
		t.Fatalf("ENGINE_FAILED 不应改写为 MISSING_VOLUME：%#v", got)
	}

	// 普通压缩包：保持「损坏或不完整」的原判断，不能误报为缺卷
	plain := filepath.Join(dir, "plain.zip")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := refineVolumeError(corrupt, plain); got != error(corrupt) {
		t.Fatalf("普通包不应改写错误：%#v", got)
	}

	// 非 apperr（如超时/取消）：原样返回
	raw := errors.New("boom")
	if got := refineVolumeError(raw, vol); got != error(raw) {
		t.Fatalf("非 apperr 应原样返回：%#v", got)
	}

	// 密码错误等其它 apperr：原样返回
	badPw := apperr.New(apperr.CodePasswordWrong, "密码错误", "")
	if got := refineVolumeError(badPw, vol); got != error(badPw) {
		t.Fatalf("非损坏类错误不应改写：%#v", got)
	}
}

func TestIsOpenFailureCode(t *testing.T) {
	for _, c := range []apperr.Code{apperr.CodeArchiveCorrupt, apperr.CodeUnsupportedFormat} {
		if !isOpenFailureCode(c) {
			t.Errorf("%s 应允许换入口重试", c)
		}
	}
	for _, c := range []apperr.Code{apperr.CodePasswordWrong, apperr.CodeMissingVolume, apperr.CodeEngineFailed, apperr.CodePathNotFound} {
		if isOpenFailureCode(c) {
			t.Errorf("%s 不应视为「打不开压缩包」", c)
		}
	}
}
