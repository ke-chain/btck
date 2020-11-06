图说比特币 Part 4:如何存储BlockHeaders？
## 测试

1. `btcwallet -C ./btcwallet.conf --create`
注：btcwallet.conf等配置文件都在源码当中。
```
Enter the private passphrase for your new wallet:
Confirm passphrase:
Do you want to add an additional layer of encryption for public data? (n/no/y/yes) [no]: no
Do you have an existing wallet seed you want to use? (n/no/y/yes) [no]: no
Your wallet generation seed is:
...
Once you have stored the seed in a safe and secure location, enter "OK" to continue: OK
Creating the wallet...
[INF] WLLT: Opened wallet
```
创建需要输入四个选项。密码设置为1，接下来no,no,OK.
2. 开启两个终端，分别启动btcd server和钱包 server
```
// Console window 1
$ btcd --configfile ./btcd.conf

// Console window 2
$ btcwallet -C ./btcwallet.con
```

3. 创建钱包用户
```
$ btcctl -C ./btcctl-wallet.conf walletpassphrase 1 3600
$ btcctl -C ./btcctl-wallet.conf createnewaccount alice
$ btcctl -C ./btcctl-wallet.conf listaccounts
{
  "alice": 0,
  "default": 0,
  "imported": 0
}
```

4. 查看miner和alice的地址
```
// Unlock your wallet first
$ btcctl -C ./btcctl-wallet.conf walletpassphrase 1 3600
$ btcctl -C ./btcctl-wallet.conf getnewaddress
MINER_ADDRESS
$ btcctl -C ./btcctl-wallet.conf getnewaddress alice
ALICE_ADDRESS
```
5. 使用miner地址重启btcd server
```
$ btcd --configfile ./btcd.conf --miningaddr=MINER_ADDRESS
```

6. 生成测试区块和交易
```
$ btcctl -C ./btcctl.conf generate 100
[...a hundred of hashes...]
$ btcctl -C ./btcctl-wallet.conf getbalance
50
```

7. 运行代码btck
``` sh
$  go run ./ --configfile ./configke.conf

```