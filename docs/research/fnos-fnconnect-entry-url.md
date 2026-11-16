# FN Connect 应用入口地址（fnDomain）

调查日期：2026-11-16。问题：托管台列表里的入口写成
`http://<当前访问域名>:<端口>/`（例如 `http://jeefyang.fnos.net:20001/`），
但用户在飞牛桌面上打开同一个代理应用时，浏览器实际落在
`https://proxy-dagu-e040-main.jeefyang.fnos.net/login`。需要弄清外网入口地址的生成规则。

## 结论

1. 飞牛桌面（FN Connect 公网访问）给每个应用入口分配一个**应用子域**，
   形如 `https://<fnDomain>.<当前域名><path>`。子域由服务端下发，前端不自己算。
2. 对本项目生成的包（入口键固定 `<appname>.main`）实测与社区实例给出的
   fnDomain 都等于「入口键小写、点换成连字符」；入口名恰为 `Application` 时
   不追加入口名（默认入口），其余入口名保留。故本项目的入口子域为
   `<appname 点→连字符>-main`。
3. FN Connect 会按子域反代到应用入口的端口，所以端口型入口（本项目代理应用
   监听 20000–29999）在公网可达，但**只在登录 FN Connect 之后**；
   未登录时子域返回 403「FN Connect 访问提示」。
4. 托管台自身走 CGI 入口，浏览器访问的域名就是禅道域（`jeefyang.fnos.net`），
   因此托管台可以根据 `location.hostname` 是否属于 FN Connect 域来切换两种入口地址。

## 证据

### 官方前端（fnOS 主站 bundle）

- `.tmp-research/fnos-ui/index-D1auxRov.js` 中的入口渲染函数（反混淆后片段）：

  ```js
  if (pe && e.fnDomain && e.port) return `https://${e.fnDomain}.${window.location.host}${e.path}`
  ```

  `pe` 表示当前是 FN Connect 访问。说明：外网时官方前端直接用服务端下发的
  `uri.fnDomain` 拼子域，不读应用自己的端口。
- 入口数据来自 WebSocket CGI `appcgi.sac.entry.v1.getEntryList`，每项
  `uri.fnDomain` 由服务端计算。
- `appcgi.netsvr.domain.list` 只用于在设置页选择 FN Connect 域名后缀，
  不是「端口 → 子域」映射表。

### 真机与社区实例

| appname | 入口键 | 实际子域 |
| --- | --- | --- |
| daili.demo | daili.demo.main | daili-demo-main.jeefyang.fnos.net |
| proxy.dagu.e040 | proxy.dagu.e040.main | proxy-dagu-e040-main.jeefyang.fnos.net（用户提供） |
| fn-it-tools | fn-it-tools.Application | fn-it-tools（tnnevol/fn-os-apps 实测） |
| motrix | motrix.Application | motrix.<account>.fnos.net（motrix-fnos 文档） |
| dsh | dsh.Application | dsh.<ID>.fnos.net（deepseek-harness-fnos 文档） |

规则与全部实例一致：入口键 `a.b.c` → 子域 `a-b-c`；`.Application` 后缀省略。

### DNS / HTTP 行为

- `daili-demo-main.jeefyang.fnos.net`、`proxy-dagu-e040-main.jeefyang.fnos.net`
  都解析到同一公网 IP（`*.jeefyang.fnos.net` 通配）。
- 未登录请求返回 403 + 标题「FN Connect 访问提示」；主域 `https://jeefyang.fnos.net/`
  返回 200。说明子域由 FN Connect 网关托管，登录后按子域转发到应用端口。

### 现成工具

- `fnos-cli app entry list`（npm `@fnnas-labs/fnos-cli`，底层
  `appcgi.sac.entry.v1.getEntryList`）返回含 `fnDomain` 的 JSON，可作为
  「拿准确 fnDomain」的正规途径；但它需要登录令牌，不适合托管台内置依赖。
- 托管台按上面的推断规则拼接即可，**并在 UI 标注「需在 FN Connect 里发布该应用」**。

## 对本项目的含义

- `internal/manager/web/app.js` 的 `entryURL(appname, port)`：
  - 当前域名属于 `fnos.net` / `5ddd.com` / `trzznas.com` 时，返回
    `https://<appname 点→连字符>-main.<当前域名>/`；
  - 否则（局域网 IP、自定义域名）仍返回 `http://<当前主机>:<端口>/`。
- 外网（FN Connect）下不再提示「端口不可达」，改为提示「入口是应用子域，
  若打不开请在 FN Connect 里发布该应用」。
- 局域网端口入口保持不变：FN Connect 只影响公网访问路径。

## 未决 / 风险

- 该规则是从官方前端行为与实例归纳的推断，没有官方文档逐字说明；
  若某天 fnOS 改变命名，托管台需要用 `getEntryList` 校正（后续增强）。
- 应用子域要求该应用已在 FN Connect 后台「发布」；未发布时子域仍 403。
  托管台只能提示，不能代用户发布。
