package diag

import (
	"strings"
	"testing"
)

// 脱敏正则要覆盖真实世界的签名参数名。以前只认 `sign`/`sig`/`auth` 这类
// 短名，`signature=`、`auth_key=`、`X-Amz-Signature=`、`hdnts=`、`md5=`、
// `expires=` 以及短 Bearer 都会原样漏进日志。
func TestSanitizeSignatureParams(t *testing.T) {
	cases := []struct {
		in  string
		bad []string
	}{
		{"signature=AAAA1111BBBB2222", []string{"AAAA1111BBBB2222"}},
		{"auth_key=abcdef99", []string{"abcdef99"}},
		{"X-Amz-Signature=DEADBEEFCAFE", []string{"DEADBEEFCAFE"}},
		{"hdnts=exp=17000~hmac=ABCDEF0123", []string{"ABCDEF0123"}},
		{"md5=ff00ee&expires=1700000000", []string{"ff00ee", "1700000000"}},
		{"Authorization: Bearer abc1", []string{"abc1"}},
	}
	for _, tc := range cases {
		out := Sanitize(tc.in)
		for _, b := range tc.bad {
			if strings.Contains(out, b) {
				t.Errorf("%q 没脱掉 %q: %q", tc.in, b, out)
			}
		}
	}
}
