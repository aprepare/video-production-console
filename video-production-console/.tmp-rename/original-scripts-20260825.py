# -*- coding: utf-8 -*-
"""Count CJK chars and split spoken lines (<=9 hanzi) for original scripts."""
from __future__ import annotations

import json
import re
from pathlib import Path

SCRIPTS = {
    "1-存款1字头": """还把钱死死捂在卡里的，先把这笔账听完。你卡里要是躺着一百万，按这一轮银行一年期整存整取的平均利率来算，一年利息刚过一万二。摊到每天，三十来块。这是个什么概念呢？家里早上一碗豆浆、一根油条，一天就见底。你以为是银行抠门。不是。存款这一侧，已经进了1字头的时代。

可你要是还在还房贷，另一侧更气人。贷款市场报价利率，一年期3%，五年期以上3.5%，连续好几个月按住不动。一边是你存进去的钱越来越薄，一边是你每月扣走的月供纹丝没松。

先听我说个现象。不是谁逼你把钱花掉，是这钱放在银行这间屋子里，待着越来越不值。

我给你算一笔账。同样一百万，一年期平均大约1.26%，一年拿一万二出头。三年期平均大约1.68%，看起来高一点，可你把这三年锁死，家里急用要提前支，利息先被罚一截。活期更难看，挂牌低到可以忽略。你说我再等等，等利率回头。问题来了。这一轮往下走的，不是某一家网点的活动价，是整张利率牌。大额存单还在卖，可额度少、门槛高，轮到你的时候经常是售罄。

你以为这是市场自己波动。其实是银行的息差被压薄了，负债端先动手。钱从你卡里看，还是那串数字。可它能换的菜、能抵的月供、能撑的年份，已经不是前几年那回事。

两种人差距就在这儿。一种人续存，到期再存，看着利息一年比一年薄，心里安慰自己本金还在。另一种人把到期的钱拆开：一截应急，一截还掉最贵的负债，一截才去想还能不能生一点。今年上半年，住户贷款整体是少的，说明有人在提前还，有人不敢再借。你站哪边，别被邻居一句话带着走。

落到家里三件事。第一，活期和一年期内的钱，只当救命，不当增值。第二，还在还的房贷，先问清楚你这笔的重定价日，别把报价没动理解成我永远降不到。第三，别把银行卡余额当成家庭安全垫的全部，急用时拿得出手的，才叫垫。

窗口不在朋友圈谁又赚到了。窗口在你下次存款到期的那一天，你是闭眼续，还是把账摊开。宏观你改变不了，这笔家庭账你得自己提。能听到这儿的人，已经比多数人多想了一步。差的那一步，是把方法看明白。主页橱窗里有《财富觉醒方法论》，五块钱，个人观点，仅供参考，不承诺收益。""",
    "2-养老金假红头": """家族群里那张红头，先别转。说今年养老金涨幅定了、某月某日统一补发、点开链接填卡号就能提前领差额。你要是家里有退休的老人，这事就冲着你来的。

先把两笔钱分开。一笔是职工基本养老金，覆盖企业和机关事业单位退休人员。政府工作报告写了，适当提高退休人员基本养老金。涨，这个方向是写进任务的。可全国那份调整通知，得到人社部、财政部官网去对原文、对文号。群里带公章的截图，很多是拿往年文件改了年份。另一笔是城乡居民基础养老金，最低标准这一轮又提了二十块，从一月一日起算，多数地方已经在补差额。这是两条线，分开核算、分开发放。你把居民那二十块，当成职工那笔已经到账，就会自己吓自己。

我希望你先看完。我也希望你能看到这里面的风险。不是养老金不涨，是假文件比真文件跑得快。发布会通报的是参保人数、基金收支、结余还在万亿这个量级上，定性是总体平稳。它不是调整文件的发布通道。历年职工养老金调整，都是人社部和财政部联合印发、单独挂网。发布时间这几年往后挪，晚几天不等于少发钱。执行起点仍从年初算，差额一次性补到社保卡金融账户。

问题来了。骗子就卡在这个空档里。红头PDF、内部名单、社区能托人多涨、链接里让你填身份证和银行卡。正规补发是系统自动核算，免申即享，啥信息都不用你交。你一点链接，卡里的钱比养老金先走。

两种人差距也在这儿。一种人天天刷短视频蹲家族群，截图转发，密码也跟着填。另一种人只认三个口子：人社部官网、财政部官网、本地人社局公众号。核不到文号的，当它不存在。资格认证别逾期，逾期只是暂时停发，补做了停发期间的钱会补回来。社保卡金融功能激活了，到时候打钱才进得去。

落到家里就三句。第一，别替老人点陌生链接。第二，别把居民基础养老金和职工养老金混成一笔。第三，通知晚发，不会把前面几个月的差额抹掉。过度乐观和过度悲观都是非理性的。宏观你改变不了，辨真假这一步你得自己提。一个家庭至少要有一个人先把这本账算明白。主页橱窗《财富觉醒方法论》，五块钱，个人观点，仅供参考，不承诺收益。""",
    "3-房子还是现金": """如果你手里有一套房、卡里还有一笔存款，现在最危险的不是选错，是用全国一句话替自己做决定。买房子更保值，还是存在银行里更保值？十年前答案很整齐。时至今日，这个问题要按你的城市重问一遍。

先看最近这一组房价。七月七十个大中城市，一线城市商品住宅销售价格环比总体上涨，二三线环比还在降，同比降幅总体继续收窄。这话什么意思？不是楼市已经翻篇，是城市和城市开始走岔路。你把一线那点环比，套到自己县城的挂牌价上，会算错。你把二三线还在降，套到核心地段的置换需求上，也会算错。

再看另一侧。存款已经进了1字头，一年期整存整取平均利率刚过1.2%。贷款市场报价利率五年期以上还停在3.5%，连续多个月没动。现金这一侧，利息薄；负债这一侧，月供没松。房子这一侧，有的城市开始横，有的城市还在磨。三件事叠在同一家人身上，才叫难。

我给你算一笔账。别先问房价会不会涨。先问三笔数。一，这套房急用时，三个月内能换成多少现金。二，月供占家里稳定收入多少，会不会把菜钱和看病钱挤掉。三，卡里的活钱，够不够撑过一次生病、一次失业、一次孩子要用钱。涨过利息几个点，那是后话。前面三笔对不上，谈保值就是自己骗自己。

你是不是听人说，贷款买房可以抗通胀？可你算过没有？抗通胀的是能租出去、能住进去、急用时还能出手的那套。抗不了通胀的，是空关着、挂牌没人问、还要交物业和利息的那套。现金也一样。现金能救命，但按这一轮存款利率，它跑不赢家里日常开销往上走的那截。

两种人会慢慢分开。一种人听风就动，今天想卖房All in余额，明天又想把存款砸进一套学区。另一种人把资产拆开：一套用来住或者收租，一截现金托底，负债只留自己扛得住的。赢家首先想到的永远都是风险，输家首先想到的永远都是赚钱。

落到家庭就一句。你的城市、你的房子、你的现金流，分开看，别让短视频里的全国结论替你签字。在你做决定的那一刻，百分之九十的结果就已经定了。能听到这儿的人，已经比百分之九十的人多想了一步。你只差最后一步。主页橱窗《财富觉醒方法论》，五块钱，个人观点，仅供参考，不承诺收益。""",
}


def cjk_count(text: str) -> int:
    return sum(1 for ch in text if "\u4e00" <= ch <= "\u9fff")


def split_lines(text: str) -> list[str]:
    text = re.sub(r"\s+", "", text)
    # keep punctuation as part of speech flow; split by clause punct first
    parts = re.split(r"(?<=[，。！？；：、])", text)
    lines: list[str] = []
    buf = ""
    for part in parts:
        if not part:
            continue
        # further split long clauses
        chunks = [part]
        rebuilt = []
        for chunk in chunks:
            if cjk_count(chunk) <= 9:
                rebuilt.append(chunk)
                continue
            # split by natural phrases of <=9
            cur = ""
            for ch in chunk:
                nxt = cur + ch
                if cjk_count(nxt) > 9:
                    if cur:
                        rebuilt.append(cur)
                    cur = ch
                else:
                    cur = nxt
            if cur:
                rebuilt.append(cur)
        for piece in rebuilt:
            if not piece:
                continue
            if buf and cjk_count(buf + piece) <= 9:
                buf += piece
            else:
                if buf:
                    lines.append(buf)
                buf = piece
    if buf:
        lines.append(buf)
    # strip leftover latin-only? keep as is
    return [ln for ln in lines if ln]


def validate(lines: list[str], source: str) -> dict:
    joined = "".join(lines)
    src = re.sub(r"\s+", "", source)
    overs = [(i + 1, ln, cjk_count(ln)) for i, ln in enumerate(lines) if cjk_count(ln) > 9]
    starters = [(i + 1, ln) for i, ln in enumerate(lines) if ln[:1] in "的了是和在而但就"]
    singles = [(i + 1, ln) for i, ln in enumerate(lines) if cjk_count(ln) <= 1]
    return {
        "chars": cjk_count(source),
        "lines": len(lines),
        "max_line": max((cjk_count(ln) for ln in lines), default=0),
        "lossless": joined == src,
        "overs": overs[:12],
        "starters": starters[:12],
        "singles": singles[:12],
    }


out = {}
for name, script in SCRIPTS.items():
    lines = split_lines(script)
    info = validate(lines, script)
    out[name] = {"info": info, "lines": lines, "script": script}
    print(name, info)

Path(r"C:/Users/prepare/.codex/worktrees/视频号混剪/video-production-console/video-production-console/.tmp-rename/original-scripts-20260825.json").write_text(
    json.dumps({k: {"chars": v["info"]["chars"], "lines": v["lines"]} for k, v in out.items()}, ensure_ascii=False, indent=2),
    encoding="utf-8",
)
