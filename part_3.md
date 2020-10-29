图说比特币 Part 3:如何加载配置文件？
======
> 备注：为了简洁起见，文章只涉及了部分关键代码和结构。推荐下载源码，查看详细实现。

## 1.1引言
之前我们已经能接收和发送"handshake"和"心跳"，但是我们的地址是写死的。
[BTCD](https://github.com/btcsuite/btcd)只要运行命令` btcd --configfile ./btcd.conf` 就完成了所有配置。是如何做到的呢？
今天的目标就是就将地址(127.0.0.1:9333)放入配置文件当中。


## 1.2代码地址
[图说比特币 Part 3:如何加载配置文件？](https://github.com/ke-chain/btck/tree/part_3)

## 1.3 加载配置文件

![](./images/Activityloadconfig.png)
加载配置文件使用了 [go-flags](github.com/jessevdk/go-flags)，用于解析命令行和文件，是内置库flag的加强版。

1. 新建默认配置`cfg := config{LogDir:   defaultLogDir,MaxPeers: defaultMaxPeers,}`

2. 加载命令行`preParser.Parse()`
3. 解析配置文件`flags.NewIniParser(parser).ParseFile(preCfg.ConfigFile)`
4. 设置其它配置项`cfg.dial = net.DialTimeout`   `cfg.lookup = net.LookupIP`

## 1.4 [BTCD](https://github.com/btcsuite/btcd)如何载入配置的地址？

![](./images/Main2Sync.png)

1. `newServer` 新建了`server`，并且连接连接所有配置的地址。
   1.1 新建`server`：`s := server{} `
   1.2 获取配置的地址：`permanentPeers := cfg.ConnectPeers`
   1.3 连接所有地址：
    ```go
    go s.connManager.Connect(&connmgr.ConnReq{
			Addr:      netAddr,
			Permanent: true,
        })
    ```
2. `Connect` :为连接指定id并且发送连接请求。
   2.1 为连接指定id：`atomic.StoreUint64(&c.id, atomic.AddUint64(&cm.connReqCount, 1))` 
   2.2 发送连接请求：`conn, err := cm.cfg.Dial(c.Addr)`

3. `outboundPeerConnected`：建立网络连接并进行"initial handshake"。("initial handshake"就是第一章的Version和Verack信息)
   3.1 建立网络连接：`connManager`建立连接后会调用`outboundPeerConnected`。该函数初始化一个新的`serverPeer`(`serverPeer`是`peer`的子类，多了`server`的状态信息)。
   3.2 "initial handshake"：sp.AssociateConnection(conn)

4到10就是处理"initial handshake"的过程，11：`startSync`是同步区块信息，目前暂时为空以后会实现。


## 1.5测试：

```bash
$ go run ./ --configfile ./configke.conf
2020-10-26 15:38:22.842 [TRC] SRVR: Starting server
2020-10-26 15:38:22.842 [TRC] SYNC: Starting sync manager
2020-10-26 15:38:22.842 [TRC] CMGR: Connection manager started
...
```

## 1.6总结

本章先是使用 [go-flags](github.com/jessevdk/go-flags)解析命令行和配置文件，然后将实现可btcd从启动到建立网络连接的过程。
