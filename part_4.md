图说比特币 Part 4:如何存储BlockHeaders？
======
> 备注：为了简洁起见，文章只涉及了部分关键代码和结构。推荐下载源码，查看详细实现。

## 1.1引言
part 1 完成了"handshake"，part 2 "心跳"信息，part 3 加载配合文件。
接下来我们存储BlockHeaders，以后可以用BlockHeaders验证交易。

## 1.2代码地址
[图说比特币 Part 4:如何存储BlockHeaders？](https://github.com/ke-chain/btck/tree/part_4)



## 1.3 BoltDB数据库
>Bolt 是一个纯键值存储的 Go 数据库，启发自 Howard Chu 的 LMDB. 它旨在为那些无须一个像 Postgres 和 MySQL 这样有着完整数据库服务器的项目，提供一个简单，快速和可靠的数据库。

**BoltDB**足够简单，而且是go实现的。它使用**键值存储数据**，可以理解成一个存在文件中的map。

## 1.4 数据库结构
### Headers表
key|value
---|:--:
BlockHash|StoredHeader

Headers表存储所有Header的数据，key是block的hash，value是`StoredHeader`。
`StoredHeader`包含了blockheader，当前高度和难度值的总和。

### ChainTip表
key|value
---|:--:
"KEYChainTip"|StoredHeader

ChainTip表存储最新的区块头。


## 1.5 四种网络模式

**MainNet**：主网。真实的网络，real money。详细参数看`chaincfg.MainNetParams`

**TestNet**：测试网络。是互联网上的另一个“比特币区块链”，通过指定命令行参数 --testnet（或者在bitcoin.conf配置文件中添加testnet=1）启动，区块大小10-20GB，它使得开发、测试人员在不需要使用real money的情况下，近乎真实的体验比特币网络。详细参数看`chaincfg.TestNet3Params`

**SimNet**：模拟测试网络。通过指定命令行参数 --simnet启动(配置文件中添加simnet=1），节点不会和其他节点通讯，如果节点运行在SimNet，程序创建全新的区块链，不需要区块数据同步，主要用于应用开发和测试的目的。。详细参数看`chaincfg.SimNetParams`

**RegTest**：回归测试网络。通过指定命令行参数 --regtest(配置文件中添加regtest=1） 在本机启动一个私有节点，主要用于应用开发和测试的目的。详细参数看`chaincfg.RegressionNetParams`

今天我们要使用**RegTest**来测试本地Header的存储和查询，**TestNet**来测试计算工作难度(PoW target)。

## 1.6 测试
```
# cd ./blockchain
# go test .
ok
```

选择`TestBlockchain_CommitHeader`方法作为例子，它验证`CommitHeader`方法。
1. `NewBlockchainSPV` 新建一个数据库用于存储headers
2. `CommitHeader` 存储区块头,区块信息写死在`chain`中
3. `GetBestHeader` 从数据库中提取区块头
4. 比较`chain`和`best`中的数据，如果相等则表示成功



## 1.7 总结

本章应用了BoltDB数据库和单元测试来存储headers。接下来我们就可以获取真正的headers。

参考：
[spvwallet](github.com/OpenBazaar/spvwallet)
[BTCD](https://github.com/btcsuite/btcd)