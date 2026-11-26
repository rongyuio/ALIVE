# 0013 黑边区的模糊底：照 B 站的实现

**2026-11-23。** 画面按 contain 缩进舞台之后，空出来的黑边不再铺纯黑，
而是铺一张模糊的静态图。

这一篇的价值不在结论，在**参数是从哪来的**。前两版都是看着截图猜的，
两次都错：一次猜成「B 站没有这一层」，一次猜成「每帧实时模糊视频帧」。
第三版把播放器的 bundle 下载下来读了原文。

**这条线**：不在任何一条线上——一天解决，之后没回头（见
[evolution.md](../evolution.md) 末尾）。它的用处是方法：**参数别猜，去读实现**。

## 原始材料

```sh
# 网页端播放器
curl -A "<Chrome UA>" -e https://live.bilibili.com/ \
  https://s1.hdslb.com/bfs/static/bilibili-live-player/room-player.prod.min.js

# H5 播放器（竖屏）
curl -A "<iPhone UA>" https://live.bilibili.com/h5/<房间号>
# → //s1.hdslb.com/bfs/blive-engineer/live-web-player/mobile-player.prod.min.js
# → //s1.hdslb.com/bfs/static/blive/blfe-live-h5/static/js/room.<hash>.js
# → //s1.hdslb.com/bfs/static/blive/blfe-live-h5/static/css/room.<hash>.css
```

`room-player.prod.min.js` 里的原文（模块 2421，导出 `Qp`）：

```js
function F(e){                                  // e 是 video 元素
  if (e.videoHeight > e.videoWidth) {           // 只在竖屏源
    const t = U(e);                             // canvas 抓帧 → JPEG 0.8
    i.style.cssText = `
      background-image: url(${t});
      background-size: cover;
      background-position: center;
      width: 100%; height: 100%;
      filter: blur(35px);
      position: absolute; top:0;bottom:0;left:0;right:0;`;
    e.insertAdjacentElement("afterend", t);
```

调用点只有一个，在 `FirstFrame` 事件里，抓一次就完事。

H5 那边是另一条路（`room.<hash>.css` + `room.<hash>.js`）：

```css
.background-img{position:fixed;top:0;right:0;left:0;bottom:0;
  background-size:cover;background-repeat:no-repeat;background-position:50%}
.bgBlur{filter:blur(.266667rem);transform:scale(1.2)}
.background-cover{width:100%;height:100%;position:fixed;background:rgba(0,0,0,.5)}
```

```js
backgroundImage: "url(" + e.bgUrl + ")"
// bgUrl = room_info.app_background → cover → 兜底图
```

## 三个必须照抄的细节

| 细节 | B 站的做法 | 不照抄会怎样 |
|---|---|---|
| 底图来源 | 平台给的**静态图**（`room_info.background` / `app_background`），不是视频帧 | 拿视频帧做底，它会跟着画面动——B 站的不动 |
| 模糊量 | `blur(35px)`，CSS 像素绝对值，不随窗口伸缩 | 按视口比例算，窗口一变观感就变 |
| 边缘 | `transform: scale(1.2)` 把图放大 20% | 模糊从元素外吸入透明，四边出现一圈发白的渐隐 |

还有一层 `rgba(0,0,0,.5)` 压暗（H5 的 `.background-cover`）。

## 我们的实现

- **底图**：`live.Room.Background` ← B 站 `get_info` 的 `background` 字段
  （就是 H5 的 `app_background`）。为空回落到 `Cover`。
- **绘制**：`internal/ui/player.go` 的 `stageBackdrop`。图片按舞台尺寸外扩
  20% 铺 `Fit(Cover)`，整个容器 `.Blur(35)`，上面压 `MediaBackdropScrim`。
- **令牌**：`internal/theme` 的 `MediaBackdropBlur` / `MediaBackdropOverscan` /
  `MediaBackdropScrim`，三个值逐条对应上面的源码。`player_backdrop_test.go` 里有一条
  把它们和 B 站的数比对的测试——那属于 `AGENTS.md` §4.4 点名的「常量对照常量」，
  删不删由那条规则定；这里的保证是**值本身和出处**，不是某条测试还在。
- **只在有黑边时画**：`letterbox()` 算四边宽度，≥2 DIP 才开。
- **窗口刚变过的那一帧无条件画**：`LayoutBounds` 是上一帧的布局，按它判会
  晚一帧，黑边区闪一下纯黑。

## 实测（真窗口）

1100×900 窗口、B 站 1861374457、1080P 原画、间隔 3 秒两张截图：

| 区域 | 两帧之间的平均像素差 | 亮度 |
|---|---:|---:|
| 上黑边 | **0.00** | 58.0 |
| 下黑边 | **0.00** | 65.3 |
| 画面区 | **120.36** | 99.8 → 89.8 |

黑边完全静止、画面在动。左边缘逐列亮度 79.0 / 78.5 / 80.3 / 86.7 / 94.7，
平滑过渡，没有透明渐隐。

## 试过并否掉的

| 方案 | 为什么否 |
|---|---|
| mpv 滤镜 | `vo=libmpv` 软件渲染没有后处理钩子 |
| 元素 `.Blur()` 直接糊视频帧（第二版） | 每帧重建离屏层；而且 B 站的底根本不跟着视频动 |
| 抓一帧 → 缩到 160 宽 → CPU 高斯模糊（第三版） | 思路对了（静态），但底图选错了：应该用平台给的背景图，不是视频帧 |
| 拿封面当底 | 只作退路：B 站把 `app_background` 当房间身份的一部分 |

## 不做

- 观测墙的 12 个格子：另一笔成本账。
- 竖屏源的 `capture-video-bg` 那条路（抓当前帧）：我们用平台背景图，不需要。
