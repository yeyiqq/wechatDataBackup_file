# wechatDataBackup_file

* 基于wechatDataBackup，实现PC端微信聊天记录一键转json功能。
* 导出后数据可以查看图片。

## json格式
```shell
[
  {
    "instruction": "与xxx的聊天记录",
    "dialogue": [
      {
        "index", 序号
        "speaker": "微信昵称",
        "text": "说话内容",
        "time": "说话时间"
      },
      {
        "index",
        "speaker": "",
        "text": "",
        "time": ""
      }
    ]
  }
]
```
## 使用方法
### 1. 见README.md
跑通原wechatDataBackup代码[源地址](https://github.com/git-jiadong/wechatDataBackup)。
### 2. 终端运行
```shell
go run chat_extractor_simple.go ./build/bin/User/wxid_xxxxxx
```

## 免责声明
**⚠️ 本项目仅供学习、研究使用，严禁商业使用**<br/>
**⚠️ 用于网络安全用途的，请确保在国家法律法规下使用**<br/>
**⚠️ 本项目完全免费，问你要钱的都是骗子**<br/>
**⚠️ 使用本项目初衷是作者研究微信数据库的运行使用，您使用本软件导致的后果，包含但不限于数据损坏，记录丢失等问题，作者不承担相关责任。**<br/>
**⚠️ 因软件特殊性质，请在使用时获得微信账号所有人授权，你当确保不侵犯他人个人隐私权，后果自行承担**<br/>

## 前端代码
由于前端代码不成熟，前端界面代码暂时不公开。

## 参考/引用
- 微信数据库解密和数据库的使用 [PyWxDump](https://github.com/xaoyaoo/PyWxDump/tree/master)
- silk语音消息解码 [silk-v3-decoder](https://github.com/kn007/silk-v3-decoder)
- PCM转MP3 [lame](https://github.com/viert/lame.git)
- Dat图片解码 [wechatDatDecode](https://github.com/liuggchen/wechatDatDecode)

## 交流/讨论
![](./res/wechatQR.png)