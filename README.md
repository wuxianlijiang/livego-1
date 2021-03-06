# 什么是livego<br/>     
livego是基于golang开发的rtmp服务器<br/>     
<br/>     
# 为什么基于golang
*  ## golang在语言基本支持多核CPU均衡使用，支持海量轻量级线程，提高其并发量<br/>   
   当前开源的缺陷：
   - srs只能运行在一个单核下，如果需要多核运行，只能启动多个srs监听不同的端口来提高并发量；<br/>   
   - ngx-rtmp启动多进程后，报文在多个进程内转发，需要二次开发，否则静态推送到多个子进程，效能消耗大；<br/>   
   golang在语言级别解决了上面多进程并发的问题。
*  ## 二次开发简洁快速<br/>   
   golang的开发效率远远高过C/C++

# livego支持哪些特性<br/>     
*  rtmp 推流，拉流
*  支持hls观看
*  支持http-flv观看
*  支持gop-cache缓存
*  静态relay支持：支持静态推流，拉流
*  统计信息支持：支持http在线查看流状态
## 安装
直接下载编译好的[二进制文件](https://github.com/gwuhaolin/livego/releases)后，在命令行中执行。

#### 从源码编译
1. 下载源码 `git clone https://github.com/gwuhaolin/livego.git`
2. 去 livego 目录中 执行 `go build`

## 使用
2. 启动服务：执行 `livego` 二进制文件启动 livego 服务；
3. 上行推流：通过 `RTMP` 协议把视频流推送到 `rtmp://localhost:1935/live/movie`，例如使用 `ffmpeg -re -i demo.flv -c copy -f flv rtmp://localhost:1935/live/movie` 推送；
4. 下行播放：支持以下三种播放协议，播放地址如下：
    - `RTMP`:`rtmp://localhost:1935/live/movie`
    - `FLV`:`http://127.0.0.1:7001/live/movie.flv`
    - `HLS`:`http://127.0.0.1:7002/live/movie.m3u8`


### [和 flv.js 搭配使用](https://github.com/gwuhaolin/blog/issues/3)

对Golang感兴趣？请看[Golang 中文学习资料汇总](http://go.wuhaolin.cn/)
# rtmp配置指引
## livego的rtmp配置是基于json格式，简单好用。<br/> 

```json
{
    "listen": 1935,
    "hls": "enable",
    "servers":[
        {
        "servername":"live"
        }
    ]
}
```

## 提供http动态控制流的rtmp push和pull功能。<br/> 

``` json
{
    "listen": 1935,
    "hls": "enable",
    "hlsport" : 8090,
    "httpflv" : "enable",
    "flvport" : 8011,
    "httpoper": "enable",
    "operport": 8070,
    "servers":[
        {
        "servername":"live",
        "exec_push":["./helloworld1", "./helloworld2"],
        "exec_push_done":["./helloworld1", "./helloworld2"]
        }
    ]
}
```
> 例子1:
动态pull功能, pull进来一个直播，并在服务器对外生成rtmp://127.0.0.1/live/123456的直播服务<br/>
pull开始: <br/>
curl -v "http://127.0.0.1:8070/control/pull?oper=start&app=live&name=123456&url=rtmp://live.hkstv.hk.lxdns.com/live/hks"<br/>
pull结束: <br/>
curl -v "http://127.0.0.1:8070/control/pull?oper=stop&app=live&name=123456&url=rtmp://live.hkstv.hk.lxdns.com/live/hks"

## 提供静态rtmp push功能
``` json
{
    "server":[
        {
            "appname":"live",
            "liveon":"on",
            "hlson":"on",
            "static_push":[
                "rtmp://alpush.xxxx.cn/live",
                "rtmp://alpush.xxxx.cn/xxxx"
            ]
        }
    ]
}
```
> 在配置livego.cfg的json中，static_push字段后面的为rtmp push地址列表
> 配置后，任何push上来的流，都会push音视频到配置的push地址列表相关服务器中
