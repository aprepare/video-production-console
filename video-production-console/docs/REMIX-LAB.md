# 文案进化台（Remix Lab）

把「试提示词 → 改提示词 → 采用为生产系统提示词」收进控制台。

## 能力

1. **提示词目录**：内置多套财经口播二创策略（含当前生产定稿）。
2. **选择 / 编辑**：可改 system / user 再保存。
3. **采用**：设为全局系统提示词后，后续二创任务自动注入。
4. **测试**：采用后走正常项目二创即可对照；不按批注自动改提示词。

## 目录 ID

| ID | 名称 | 说明 |
|----|------|------|
| elder_stable | 中老年定稿（生产默认） | 文案进化台 2026-08-19：六件套机器、数字原词、中老年钩子、禁第N个难题 |
| wash | 洗稿 | 保顺序数字例子，只改气口 |
| bone_flesh | 骨肉分离 | 只借骨架，皮肉全换 |
| gene_clone | 爆款基因复刻 | 保留情绪曲线与爆款逻辑 |
| hook_types | 钩子类型白名单 | 复用钩子类型、强制换句子 |
| emotion_wave | 情绪波浪 | 每隔几句小高潮，完播向 |

## 存储

- 全局采用：`{data_root}/remix_lab/active_prompt.json`
- 任务注入：manifest `non_secret_settings.remix_system_prompt` / `remix_user_prompt`
- `remix_prompt_style` 仍为 `rewrite` | `wash`（设置页不变）

## API

- `GET /api/remix-lab/prompts` — 目录（含完整 system/user）
- `GET /api/remix-lab/active-prompt` — 当前采用
- `PUT /api/remix-lab/active-prompt` — 采用或清空（body 空字段 = 恢复内置）

## 刻意不做

- 不按批注自动改提示词
- 不把多套策略塞进设置页下拉（进化台是试验场）
