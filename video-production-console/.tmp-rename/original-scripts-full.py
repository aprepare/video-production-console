# -*- coding: utf-8 -*-
from __future__ import annotations
import json, re
from pathlib import Path

S1 = """还把钱死死捂在卡里的，先把这笔账听完。你卡里要是躺着一百万，按这一轮银行一年期整存整取的平均利率来算，一年利息刚过一万二。摊到每天，三十来块。这是个什么概念呢？家里早上一碗豆浆、一根油条，一天就见底。你以为是银行抠门。不是。存款这一侧，已经进了1字头的时代。

可你要是还在还房贷，另一侧更气人。贷款市场报价利率，一年期3%，五年期以上3.5%，连续好几个月按住不动。一边是你存进去的钱越来越薄，一边是你每月扣走的月供纹丝没松。同一家人，同一张银行卡，进出两头都在跟你较劲。

先听我说个现象。不是谁逼你把钱花掉，是这钱放在银行这间屋子里，待着越来越不值。你去网点看利率牌，一年、两年、三年，数字都矮了一截。大额存单还在卖，可额度少、门槛高，轮到你的时候经常是售罄。你想锁一个稍高的价，窗口不一定轮得上。

我给你算一笔账。同样一百万，一年期平均大约1.26%，一年拿一万二出头。三年期平均大约1.68%，看起来高一点，可你把这三年锁死，家里急用要提前支，利息先被罚一截。活期更难看，挂牌低到可以忽略。十万块活期放一年，利息换不了一袋米。你说我再等等，等利率回头。问题来了。这一轮往下走的，不是某一家网点的活动价，是整张利率牌。你等到的，多半不是回头，是更薄的一档。

你以为这是市场自己波动。其实是银行的息差被压薄了，负债端先动手。钱从你卡里看，还是那串数字。可它能换的菜、能抵的月供、能撑的年份，已经不是前几年那回事。今年上半年，住户存款还在增加，可住户贷款整体是少的。有人在提前还，有人不敢再借。钱没有消失，它在家里换位置。你要是还只用“本金还在”安慰自己，你会慢半拍。

两种人差距就在这儿。一种人续存，到期再存，看着利息一年比一年薄，心里安慰自己本金还在。另一种人把到期的钱拆开：一截应急，够三个月家里开销；一截还掉最贵的负债，月供先松一口气；一截才去想还能不能生一点。先进场的不是去赌，是先把家里的漏洞补上。跟着别人买的，往往补的是别人的漏洞。

落到家里三件事。第一，活期和一年期内的钱，只当救命，不当增值。第二，还在还的房贷，先问清楚你这笔的重定价日，别把报价没动理解成我永远降不到。第三，别把银行卡余额当成家庭安全垫的全部，急用时拿得出手的，才叫垫。房子、存款、月供，三张单子摊开看，别让邻居一句话替你签字。

窗口不在朋友圈谁又赚到了。窗口在你下次存款到期的那一天，你是闭眼续，还是把账摊开。在你做决定的那一刻，百分之九十的结果就已经定了。宏观你改变不了，这笔家庭账你得自己提。能听到这儿的人，已经比多数人多想了一步。差的那一步，是把方法看明白。主页橱窗里有《财富觉醒方法论》，五块钱，个人观点，仅供参考，不承诺收益。"""

S2 = """家族群里那张红头，先别转。说今年养老金涨幅定了、某月某日统一补发、点开链接填卡号就能提前领差额。你要是家里有退休的老人，这事就冲着你来的。假文件比真文件跑得快，跑的就是你卡里那点养老钱。

先把两笔钱分开。一笔是职工基本养老金，覆盖企业和机关事业单位退休人员。政府工作报告写了，适当提高退休人员基本养老金。涨，这个方向是写进任务的。可全国那份调整通知，得到人社部、财政部官网去对原文、对文号。群里带公章的截图，很多是拿往年文件改了年份。另一笔是城乡居民基础养老金，最低标准这一轮又提了二十块，从一月一日起算，多数地方已经在补差额。这是两条线，分开核算、分开发放。你把居民那二十块，当成职工那笔已经到账，就会自己吓自己。你把职工那笔还没挂网，当成今年不涨了，也会自己吓自己。

我希望你先看完。我也希望你能看到这里面的风险。不是养老金不涨，是空档被骗子盯上了。人社部例行发布会通报的是参保人数、基金收支、结余还在万亿这个量级上，定性是总体平稳。它不是调整文件的发布通道。历年职工养老金调整，都是人社部和财政部联合印发、单独挂网。发布时间这几年往后挪，晚几天不等于少发钱。执行起点仍从年初算，差额一次性补到社保卡金融账户。通知晚发，前面几个月少发的部分，后期会补齐。

问题来了。骗子就卡在这个空档里。红头PDF、内部名单、社区能托人多涨、链接里让你填身份证和银行卡。正规补发是系统自动核算，免申即享，啥信息都不用你交。你一点链接，卡里的钱比养老金先走。还有人把规划文件、例行发布会，当成调待通知转。规划说的是未来五年方向，具体涨幅、执行时间、补发规则，还是要等每年那份通知。你转错一次，家里老人可能就信错一次。

两种人差距也在这儿。一种人天天刷短视频蹲家族群，截图转发，密码也跟着填。另一种人只认三个口子：人社部官网、财政部官网、本地人社局公众号。核不到文号的，当它不存在。资格认证别逾期，逾期只是暂时停发，补做了停发期间的钱会补回来。社保卡金融功能激活了，到时候打钱才进得去。认证这事不花钱，可你拖着，家里会空一个月。

落到家里就三句。第一，别替老人点陌生链接，也别让老人自己点。第二，别把居民基础养老金和职工养老金混成一笔。第三，通知晚发，不会把前面几个月的差额抹掉。过度乐观和过度悲观都是非理性的。宏观你改变不了，辨真假这一步你得自己提。一个家庭至少要有一个人先把这本账算明白。花错了，顶多少少称一回排骨。点错了，走的是养老钱。主页橱窗《财富觉醒方法论》，五块钱，个人观点，仅供参考，不承诺收益。"""

S3 = """如果你手里有一套房、卡里还有一笔存款，现在最危险的不是选错，是用全国一句话替自己做决定。买房子更保值，还是存在银行里更保值？十年前答案很整齐。时至今日，这个问题要按你的城市重问一遍。

先看最近这一组房价。七月七十个大中城市，一线城市商品住宅销售价格环比总体上涨，二三线环比还在降，同比降幅总体继续收窄。这话什么意思？不是楼市已经翻篇，是城市和城市开始走岔路。你把一线那点环比，套到自己县城的挂牌价上，会算错。你把二三线还在降，套到核心地段的置换需求上，也会算错。挂牌的人多，成交的人少，中间那截价差，才是你家里真正要面对的。

再看另一侧。存款已经进了1字头，一年期整存整取平均利率刚过1.2%。贷款市场报价利率五年期以上还停在3.5%，连续多个月没动。现金这一侧，利息薄；负债这一侧，月供没松。房子这一侧，有的城市开始横，有的城市还在磨。三件事叠在同一家人身上，才叫难。你要是只听“房价到底了”或者“现金最安全”，你会把三张单子揉成一句口号。

我给你算一笔账。别先问房价会不会涨。先问三笔数。一，这套房急用时，三个月内能换成多少现金。二，月供占家里稳定收入多少，会不会把菜钱和看病钱挤掉。三，卡里的活钱，够不够撑过一次生病、一次失业、一次孩子要用钱。涨过利息几个点，那是后话。前面三笔对不上，谈保值就是自己骗自己。房子能住、能租、急用能出手，才谈得上托底。空关着、挂牌没人问、还要交物业和利息，那是在烧垫。

你是不是听人说，贷款买房可以抗通胀？可你算过没有？抗通胀的是能租出去、能住进去、急用时还能出手的那套。抗不了通胀的，是把全家现金流锁死的那套。现金也一样。现金能救命，但按这一轮存款利率，它跑不赢家里日常开销往上走的那截。你把所有活钱都换成房，家里一有事就得求人。你把所有房子念头都按死，只捂存款，几年后你会发现，数字还在，能换的东西少了。

两种人会慢慢分开。一种人听风就动，今天想卖房All in余额，明天又想把存款砸进一套学区。另一种人把资产拆开：一套用来住或者收租，一截现金托底，负债只留自己扛得住的。赢家首先想到的永远都是风险，输家首先想到的永远都是赚钱。你不用成为赢家里最聪明的那个，你只要别成为输家里动作最快的那个。

落到家庭就一句。你的城市、你的房子、你的现金流，分开看，别让短视频里的全国结论替你签字。在你做决定的那一刻，百分之九十的结果就已经定了。能听到这儿的人，已经比百分之九十的人多想了一步。你只差最后一步。主页橱窗《财富觉醒方法论》，五块钱，个人观点，仅供参考，不承诺收益。"""


def cjk_count(text: str) -> int:
    return sum(1 for ch in text if "\u4e00" <= ch <= "\u9fff")


KEEP = re.compile(r"^\d+(?:\.\d+)?(?:万|亿|元|块|年|月|日|天|个|期|%|％)?$")
BAD_START = set("的了是和在而但就把被让从对把")


def clauses(text: str) -> list[str]:
    text = re.sub(r"\s+", "", text)
    parts = re.split(r"(?<=[，。！？；])", text)
    return [p for p in parts if p]


def pack(clause: str) -> list[str]:
    n = cjk_count(clause)
    if n <= 9:
        return [clause]
    # prefer split before punctuation-ending chunks of <=9
    lines, buf = [], ""
    i = 0
    chars = list(clause)
    while i < len(chars):
        ch = chars[i]
        cand = buf + ch
        if cjk_count(cand) <= 9:
            buf = cand
            i += 1
            continue
        # don't leave 1-2 chars if possible: look back for natural break
        if buf:
            lines.append(buf)
            buf = ""
        else:
            buf = ch
            i += 1
    if buf:
        if lines and cjk_count(lines[-1] + buf) <= 9:
            lines[-1] += buf
        else:
            lines.append(buf)
    # merge tiny leftovers into previous
    merged = []
    for ln in lines:
        if merged and cjk_count(ln) <= 2 and cjk_count(merged[-1] + ln) <= 9:
            merged[-1] += ln
        else:
            merged.append(ln)
    return merged


def fix_starters(lines: list[str]) -> list[str]:
    out = []
    for ln in lines:
        if out and ln and ln[0] in BAD_START:
            if cjk_count(out[-1] + ln) <= 9:
                out[-1] += ln
                continue
            # move first char back if previous can take it
            if cjk_count(out[-1] + ln[0]) <= 9:
                out[-1] += ln[0]
                ln = ln[1:]
                if not ln:
                    continue
        out.append(ln)
    return out


def split_lines(text: str) -> list[str]:
    lines: list[str] = []
    for c in clauses(text):
        lines.extend(pack(c))
    lines = fix_starters(lines)
    # drop empty
    lines = [ln for ln in lines if ln]
    # merge remaining singles
    merged = []
    for ln in lines:
        if merged and cjk_count(ln) <= 1 and cjk_count(merged[-1] + ln) <= 9:
            merged[-1] += ln
        else:
            merged.append(ln)
    return fix_starters(merged)


def validate(lines, source):
    src = re.sub(r"\s+", "", source)
    joined = "".join(lines)
    overs = [(i+1, ln, cjk_count(ln)) for i, ln in enumerate(lines) if cjk_count(ln) > 9]
    starters = [(i+1, ln) for i, ln in enumerate(lines) if ln[:1] in BAD_START]
    singles = [(i+1, ln) for i, ln in enumerate(lines) if cjk_count(ln) <= 1]
    return {
        "chars": cjk_count(source),
        "lines": len(lines),
        "max": max((cjk_count(x) for x in lines), default=0),
        "lossless": joined == src,
        "overs": overs[:8],
        "starters": starters[:8],
        "singles": singles[:8],
    }

out_dir = Path(r"C:/Users/prepare/.codex/worktrees/视频号混剪/video-production-console/video-production-console/.tmp-rename/original-20260825")
out_dir.mkdir(parents=True, exist_ok=True)

scripts = {
    "01-存款1字头对房贷": S1,
    "02-养老金假红头": S2,
    "03-房子还是现金": S3,
}
report = {}
for name, script in scripts.items():
    lines = split_lines(script)
    info = validate(lines, script)
    report[name] = info
    (out_dir / f"{name}.txt").write_text(script.strip() + "\n", encoding="utf-8")
    (out_dir / f"{name}-口播.txt").write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(name, info)

(out_dir / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
print("DIR", out_dir)
