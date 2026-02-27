# IPTV Scraper

实现 IPTV 机顶盒的认证流程，自动获取最新的直播频道列表，并将其转换为标准的 m3u 播放列表格式。

## 获取 IPTV IP

程序需要获取 IPTV IP，你需要将项目中的 `iptv.uc` 文件放置到 OpenWrt 路由器的 `/www/cgi-bin/iptv`，并确保其具有可执行权限。
或者配置一个能返回 IPTV IP 的 URL `iptv_ip_url`。

## 配置文件说明 (Config)

程序运行依赖于 `config.json` 配置文件。

各项配置参数的含义如下：

| 字段名称 | 类型 | 说明 |
| :--- | :--- | :--- |
| `user_id` | String | 用户的宽带账号或 IPTV 专线登录用户名。 |
| `key` | String | IPTV 的认证密码或业务密码。 |
| `stb_id` | String | 机顶盒的设备 ID (STB ID)，通常可以在机顶盒背面的标签或系统设置中找到。 |
| `mac` | String | 拨号设备的 MAC 地址。 |
| `login_url` | String | IPTV 的登录认证 EPG 页面完整入口 URL。 |
| `user_agent` | String | HTTP 请求所使用的 User-Agent，尽量模拟真实机顶盒发送的请求头。 |
| `route_ip` | String | 路由器的 IP 地址，用于访问 rtp2httpd 服务。 |
| `output` | String | 成功获取频道后，生成的 m3u 播放列表输出文件名称，例如 `"iptv.m3u"`。 |
| `iptv_ip_url` | String | (可选) 获取 IPTV IP 的 URL，如果未配置则使用默认值 `http://<route_ip>/cgi-bin/iptv`。 |
| `push_url` | String | (可选) 健康检查或任务监控的回调推送 URL。程序执行成功时会向此地址发送 GET 请求 (如 Uptime Kuma)。 |

### 示例配置 (`config.json`)

```json
{
    "user_id": "iptv123456",
    "key": "123456",
    "stb_id": "00100123456789012345678900ABCDEF",
    "mac": "10:02:34:56:AB:CD",
    "login_url": "http://1.2.3.4:8080/iptvepg/platform/index.jsp",
    "user_agent": "Mozilla/5.0 (Linux; Android 9; HG888-TV) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/999.11.22.3 Safari/537.36",
    "route_ip": "192.168.1.1",
    "output": "iptv.m3u",
    "push_url": "https://uptime.example.com/api/push/your-secret-key"
}
```
