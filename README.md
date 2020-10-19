图说比特币
======
# 引言

这个系列使用**golang**从零开始写一个**比特币轻量化节点**。最终达到和实际的比特币网络进行交易和**SPV**(Simplified Payment Verification)。

# 运行

1. 安装[btcd](https://github.com/btcsuite/btcd)
2. btcd --configfile ./btcd.conf
3. go run ./