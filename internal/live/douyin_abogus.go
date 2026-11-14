package live

import "time"

// 抖音 a_bogus 反爬签名。
//
// 算法由四段拼装而成，顺序不能改：
//  1. SM3（国密哈希，不是 SHA-256）——把 query、UA、suffix 压成摘要
//  2. RC4——混淆 UA 与最终字节流
//  3. 自定义 base64 变体（s0~s4 五张表，字符集与标准 base64 不同）
//  4. 44 字节主表——含时间戳、aid、pageId 与校验和，再 RC4 并编码
//
// 校验方式只有一种：拿真实接口试。签名错一个字节，抖音会直接返回空响应。
// 这里的常量（pageId=110624、aid=6383、windowEnv、suffix="cus"）都来自
// 线上实现，不能凭"看起来合理"改写。

/* ------------------------------------------------------------------ SM3 */

// douyinSM3IV 是 SM3 标准初始寄存器值。
var douyinSM3IV = [8]uint32{
	0x7380166F, 0x4914B2B9, 0x172442D7, 0xDA8A0600,
	0xA96F30BC, 0x163138AA, 0xE38DEE4D, 0xB0FB0E4E,
}

func douyinRotl32(x uint32, n uint32) uint32 {
	n %= 32
	if n == 0 {
		return x
	}
	return (x << n) | (x >> (32 - n))
}

func douyinSM3Tj(j int) uint32 {
	if j < 16 {
		return 0x79CC4519
	}
	return 0x7A879D8A
}

func douyinSM3Ff(j int, x, y, z uint32) uint32 {
	if j < 16 {
		return x ^ y ^ z
	}
	return (x & y) | (x & z) | (y & z)
}

func douyinSM3Gg(j int, x, y, z uint32) uint32 {
	if j < 16 {
		return x ^ y ^ z
	}
	return (x & y) | (^x & z)
}

// douyinSM3Compress 压缩一个 64 字节块，就地更新寄存器。
func douyinSM3Compress(reg *[8]uint32, block []byte) {
	var w [132]uint32
	for t := 0; t < 16; t++ {
		i := 4 * t
		w[t] = uint32(block[i])<<24 | uint32(block[i+1])<<16 | uint32(block[i+2])<<8 | uint32(block[i+3])
	}
	for j := 16; j < 68; j++ {
		a := w[j-16] ^ w[j-9] ^ douyinRotl32(w[j-3], 15)
		a = a ^ douyinRotl32(a, 15) ^ douyinRotl32(a, 23)
		w[j] = a ^ douyinRotl32(w[j-13], 7) ^ w[j-6]
	}
	for j := 0; j < 64; j++ {
		w[j+68] = w[j] ^ w[j+4]
	}

	a, b, c, d := reg[0], reg[1], reg[2], reg[3]
	e, f, g, h := reg[4], reg[5], reg[6], reg[7]

	for j := 0; j < 64; j++ {
		ss1 := douyinRotl32(douyinRotl32(a, 12)+e+douyinRotl32(douyinSM3Tj(j), uint32(j)), 7)
		ss2 := ss1 ^ douyinRotl32(a, 12)
		tt1 := douyinSM3Ff(j, a, b, c) + d + ss2 + w[j+68]
		tt2 := douyinSM3Gg(j, e, f, g) + h + ss1 + w[j]

		d = c
		c = douyinRotl32(b, 9)
		b = a
		a = tt1
		h = g
		g = douyinRotl32(f, 19)
		f = e
		e = tt2 ^ douyinRotl32(tt2, 9) ^ douyinRotl32(tt2, 17)
	}

	reg[0] ^= a
	reg[1] ^= b
	reg[2] ^= c
	reg[3] ^= d
	reg[4] ^= e
	reg[5] ^= f
	reg[6] ^= g
	reg[7] ^= h
}

// douyinSM3Sum 计算 SM3 摘要（32 字节）。
func douyinSM3Sum(data []byte) []byte {
	reg := douyinSM3IV
	remaining := data

	for len(remaining) >= 64 {
		douyinSM3Compress(&reg, remaining[:64])
		remaining = remaining[64:]
	}

	// 填充：0x80、若干 0、8 字节大端比特长度
	buf := make([]byte, 0, 64)
	buf = append(buf, remaining...)
	buf = append(buf, 0x80)
	for len(buf)%64 != 56 {
		buf = append(buf, 0)
	}
	bitLength := uint64(len(data)) * 8
	buf = append(buf,
		byte(bitLength>>56), byte(bitLength>>48), byte(bitLength>>40), byte(bitLength>>32),
		byte(bitLength>>24), byte(bitLength>>16), byte(bitLength>>8), byte(bitLength))

	for i := 0; i < len(buf); i += 64 {
		douyinSM3Compress(&reg, buf[i:i+64])
	}

	out := make([]byte, 0, 32)
	for _, v := range reg {
		out = append(out, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	}
	return out
}

/* ------------------------------------------------------------------ RC4 */

// douyinRC4 做 RC4 加密：明文与密钥都按字节处理。
func douyinRC4(plaintext []byte, key []byte) []byte {
	s := make([]int, 256)
	for i := 0; i < 256; i++ {
		s[i] = i
	}
	j := 0
	for i := 0; i < 256; i++ {
		j = (j + s[i] + int(key[i%len(key)])) % 256
		s[i], s[j] = s[j], s[i]
	}

	i := 0
	j = 0
	out := make([]byte, 0, len(plaintext))
	for _, ch := range plaintext {
		i = (i + 1) % 256
		j = (j + s[i]) % 256
		s[i], s[j] = s[j], s[i]
		t := (s[i] + s[j]) % 256
		out = append(out, ch^byte(s[t]))
	}
	return out
}

/* -------------------------------------------------------- 自定义 base64 */

// 五张编码表：s0 是标准表，s1~s4 是抖音变体（字符集不同，顺序也不同）。
var douyinEncodeTables = map[string]string{
	"s0": "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/=",
	"s1": "Dkdpgh4ZKsQB80/Mfvw36XI1R25+WUAlEi7NLboqYTOPuzmFjJnryx9HVGcaStCe=",
	"s2": "Dkdpgh4ZKsQB80/Mfvw36XI1R25-WUAlEi7NLboqYTOPuzmFjJnryx9HVGcaStCe=",
	"s3": "ckdp1h4ZKsUB80/Mfvw36XIgR25+WQAlEi7NLboqYTOPuzmFjJnryx9HVGDaStCe",
	"s4": "Dkdpgh2ZmsQB80/MfvV36XI1R45-WUAlEixNLwoqYTOPuzKFjJnry79HbGcaStCe",
}

var (
	douyinEncodeMasks  = [4]uint32{16515072, 258048, 4032, 63}
	douyinEncodeShifts = [4]uint32{18, 12, 6, 0}
)

// douyinLongInt 取第 round 组的 3 个字节拼成 24 位整数（不足补 0）。
func douyinLongInt(round int, data []byte) uint32 {
	i := round * 3
	var b1, b2, b3 uint32
	if i < len(data) {
		b1 = uint32(data[i])
	}
	if i+1 < len(data) {
		b2 = uint32(data[i+1])
	}
	if i+2 < len(data) {
		b3 = uint32(data[i+2])
	}
	return (b1 << 16) | (b2 << 8) | b3
}

// douyinEncode 用指定表把字节编码成字符串。
func douyinEncode(data []byte, table string) string {
	t := douyinEncodeTables[table]
	out := make([]byte, 0, len(data)*4/3+4)
	totalChars := (len(data) / 3) * 4
	if len(data)%3 != 0 {
		totalChars += 4
	}

	round := 0
	longInt := douyinLongInt(0, data)
	for i := 0; i < totalChars; i++ {
		if i/4 != round {
			round++
			longInt = douyinLongInt(round, data)
		}
		idx := i % 4
		out = append(out, t[(longInt&douyinEncodeMasks[idx])>>douyinEncodeShifts[idx]])
	}
	return string(out)
}

/* ------------------------------------------------------------- 随机串 */

// douyinGenerRandom 生成 4 字节的"随机"字节。
func douyinGenerRandom(randomNum int32, option [2]int32) []byte {
	byte1 := randomNum & 255
	byte2 := (randomNum >> 8) & 255
	return []byte{
		byte((byte1 & 170) | (option[0] & 85)),
		byte((byte1 & 85) | (option[0] & 170)),
		byte((byte2 & 170) | (option[1] & 85)),
		byte((byte2 & 85) | (option[1] & 170)),
	}
}

// douyinRandomPrefix 返回固定的 12 字节前缀。
//
// 名字叫"随机"，实际用的是三个写死的浮点常量，因此每次输出一样。
// 保持与线上实现一致比"真的随机"更重要——换个前缀会让签名整体对不上。
func douyinRandomPrefix() []byte {
	vals := []float64{0.123456789, 0.987654321, 0.555555555}
	out := make([]byte, 0, 12)
	out = append(out, douyinGenerRandom(int32(vals[0]*10000.0), [2]int32{3, 45})...)
	out = append(out, douyinGenerRandom(int32(vals[1]*10000.0), [2]int32{1, 0})...)
	out = append(out, douyinGenerRandom(int32(vals[2]*10000.0), [2]int32{1, 5})...)
	return out
}

/* ------------------------------------------------------------ 主体拼装 */

// douyinSplitToBytes 把 i64 拆成 4 个大端字节。
func douyinSplitToBytes(num int64) [4]int64 {
	return [4]int64{
		(num >> 24) & 255,
		(num >> 16) & 255,
		(num >> 8) & 255,
		num & 255,
	}
}

// douyinRC4Body 构造 44 项主表并做 RC4。
//
// suffix 恒为 "cus"、arguments 恒为 [0,1,14]，线上实现就是写死的。
func douyinRC4Body(query, userAgent, windowEnv, suffix string, arguments [3]int32, startTime int64) []byte {
	// 双重 SM3：线上 Python 版本就是这么算的，单次哈希结果不同
	urlList := douyinSM3Sum(douyinSM3Sum([]byte(query + suffix)))
	cusOnce := douyinSM3Sum([]byte(suffix))
	cus := douyinSM3Sum(cusOnce)
	uaKey := []byte{0, 1, 14}
	ua := douyinSM3Sum([]byte(douyinEncode(douyinRC4([]byte(userAgent), uaKey), "s3")))

	endTime := startTime + 100

	b := make([]int64, 80)
	b[8] = 3
	b[10] = endTime
	b[16] = startTime
	b[18] = 44

	stBytes := douyinSplitToBytes(b[16])
	b[20], b[21], b[22], b[23] = stBytes[0], stBytes[1], stBytes[2], stBytes[3]
	b[24] = (b[16] / 256 / 256 / 256 / 256) & 255
	b[25] = (b[16] / 256 / 256 / 256 / 256 / 256) & 255

	arg0Bytes := douyinSplitToBytes(int64(arguments[0]))
	b[26], b[27], b[28], b[29] = arg0Bytes[0], arg0Bytes[1], arg0Bytes[2], arg0Bytes[3]

	b[30] = int64(arguments[1]/256) & 255
	b[31] = int64(arguments[1]%256) & 255

	arg1Bytes := douyinSplitToBytes(int64(arguments[1]))
	b[32], b[33] = arg1Bytes[0], arg1Bytes[1]

	arg2Bytes := douyinSplitToBytes(int64(arguments[2]))
	b[34], b[35], b[36], b[37] = arg2Bytes[0], arg2Bytes[1], arg2Bytes[2], arg2Bytes[3]

	b[38] = douyinByteAt(urlList, 21)
	b[39] = douyinByteAt(urlList, 22)
	b[40] = douyinByteAt(cus, 21)
	b[41] = douyinByteAt(cus, 22)
	b[42] = douyinByteAt(ua, 23)
	b[43] = douyinByteAt(ua, 24)

	etBytes := douyinSplitToBytes(b[10])
	b[44], b[45], b[46], b[47] = etBytes[0], etBytes[1], etBytes[2], etBytes[3]
	b[48] = b[8]
	b[49] = (b[10] / 256 / 256 / 256 / 256) & 255
	b[50] = (b[10] / 256 / 256 / 256 / 256 / 256) & 255

	const pageID = 110624
	b[51] = pageID
	pageBytes := douyinSplitToBytes(pageID)
	b[52], b[53], b[54], b[55] = pageBytes[0], pageBytes[1], pageBytes[2], pageBytes[3]

	const aid = 6383
	b[56] = aid
	b[57] = aid & 255
	b[58] = (aid >> 8) & 255
	b[59] = (aid >> 16) & 255
	b[60] = (aid >> 24) & 255

	windowEnvBytes := []byte(windowEnv)
	b[64] = int64(len(windowEnvBytes))
	b[65] = int64(len(windowEnvBytes)) & 255
	b[66] = int64(len(windowEnvBytes)) >> 8 & 255
	b[69] = 0
	b[70] = 0
	b[71] = 0

	checksum := b[18] ^ b[20] ^ b[26] ^ b[30] ^ b[38] ^ b[40] ^ b[42] ^
		b[21] ^ b[27] ^ b[31] ^ b[35] ^ b[39] ^ b[41] ^ b[43] ^
		b[22] ^ b[28] ^ b[32] ^ b[36] ^
		b[23] ^ b[29] ^ b[33] ^ b[37] ^
		b[44] ^ b[45] ^ b[46] ^ b[47] ^ b[48] ^ b[49] ^ b[50] ^
		b[24] ^ b[25] ^ b[52] ^ b[53] ^ b[54] ^ b[55] ^
		b[57] ^ b[58] ^ b[59] ^ b[60] ^ b[65] ^ b[66] ^ b[70] ^ b[71]
	b[72] = checksum

	bb := []byte{
		byte(b[18]), byte(b[20]), byte(b[52]), byte(b[26]), byte(b[30]), byte(b[34]),
		byte(b[58]), byte(b[38]), byte(b[40]), byte(b[53]), byte(b[42]), byte(b[21]),
		byte(b[27]), byte(b[54]), byte(b[55]), byte(b[31]), byte(b[35]), byte(b[57]),
		byte(b[39]), byte(b[41]), byte(b[43]), byte(b[22]), byte(b[28]), byte(b[32]),
		byte(b[60]), byte(b[36]), byte(b[23]), byte(b[29]), byte(b[33]), byte(b[37]),
		byte(b[44]), byte(b[45]), byte(b[59]), byte(b[46]), byte(b[47]), byte(b[48]),
		byte(b[49]), byte(b[50]), byte(b[24]), byte(b[25]), byte(b[65]), byte(b[66]),
		byte(b[70]), byte(b[71]),
	}
	bb = append(bb, windowEnvBytes...)
	bb = append(bb, byte(checksum))

	// 最后用单字节密钥 'y' 再 RC4
	return douyinRC4(bb, []byte{'y'})
}

func douyinByteAt(data []byte, i int) int64 {
	if i < len(data) {
		return int64(data[i])
	}
	return 0
}

// 固定的窗口环境指纹（线上实现写死）。
const douyinWindowEnv = "1920|1080|1920|1040|0|30|0|0|1872|92|1920|1040|1857|92|1|24|Win32"

// suffix 与 arguments 是线上写死的两个常量。
const douyinSignSuffix = "cus"

var douyinSignArguments = [3]int32{0, 1, 14}

// douyinABogus 生成 a_bogus 签名值（含结尾的 "="）。
//
// query 是 URL 上除 a_bogus 之外的全部参数串，必须与最终发出去的串
// 逐字节一致；userAgent 必须与请求头里的 User-Agent 完全一致，
// 否则服务端重算出的摘要对不上，接口返回空数据。
// now 由调用方传入，测试里可以固定时间戳做向量比对。
func douyinABogus(query, userAgent string, now time.Time) string {
	body := douyinRC4Body(query, userAgent, douyinWindowEnv, douyinSignSuffix, douyinSignArguments, now.UnixMilli())
	combined := append(douyinRandomPrefix(), body...)
	return douyinEncode(combined, "s4") + "="
}
