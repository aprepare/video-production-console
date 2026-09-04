import type { FormEvent } from "react";
import type { Account } from "../types";

type AccountSwitcherProps = {
  accounts: Account[];
  selectedAccountID: string;
  onSelectAccount: (accountID: string) => void;
  formOpen: boolean;
  onToggleForm: () => void;
  onCreate: (event: FormEvent) => void;
  newAccountName: string;
  onNewAccountNameChange: (value: string) => void;
  backgroundSelected: boolean;
  onBackgroundChange: (file: File | null) => void;
  /** 打开选中账号的制作配置（专属混剪样式、配音音色）。 */
  onConfigureAccount?: (account: Account) => void;
};

export function AccountSwitcher({
  accounts,
  selectedAccountID,
  onSelectAccount,
  formOpen,
  onToggleForm,
  onCreate,
  newAccountName,
  onNewAccountNameChange,
  backgroundSelected,
  onBackgroundChange,
  onConfigureAccount,
}: AccountSwitcherProps) {
  const selectedAccount = accounts.find((item) => item.id === selectedAccountID);
  return (
    <aside>
      <nav className="account-nav" aria-labelledby="account-nav-title">
        <div className="aside-title" id="account-nav-title">
          账号 <span>{accounts.length}</span>
        </div>
        <div className="account-list">
          <button
            className={!selectedAccountID ? "selected" : ""}
            aria-current={!selectedAccountID ? "page" : undefined}
            onClick={() => onSelectAccount("")}
          >
            全部账号
          </button>
          {accounts.map((item) => (
            <button
              key={item.id}
              className={selectedAccountID === item.id ? "selected" : ""}
              aria-current={selectedAccountID === item.id ? "page" : undefined}
              onClick={() => onSelectAccount(item.id)}
            >
              {item.name}
            </button>
          ))}
        </div>
        {onConfigureAccount ? (
          <button
            type="button"
            className="account-manage-toggle"
            disabled={!selectedAccount}
            title={selectedAccount ? `配置 ${selectedAccount.name} 的专属样式和音色` : "先选中一个账号"}
            onClick={() => selectedAccount && onConfigureAccount(selectedAccount)}
          >
            账号配置{selectedAccount?.overrides ? " ·已定制" : ""}
          </button>
        ) : null}
        <button
          type="button"
          className="account-manage-toggle"
          aria-expanded={formOpen}
          aria-controls="account-create-form"
          onClick={onToggleForm}
        >
          {formOpen ? "收起账号管理" : "新增账号"}
        </button>
        <form
          id="account-create-form"
          onSubmit={onCreate}
          className={`add-account${formOpen ? " add-account--open" : ""}`}
        >
          <label htmlFor="new-account-name">账号名称</label>
          <input
            id="new-account-name"
            value={newAccountName}
            onChange={(event) => onNewAccountNameChange(event.target.value)}
            placeholder="添加账号名称"
          />
          <label className="background-pick">
            {backgroundSelected ? "已选择背景图" : "选择固定背景图"}
            <input
              type="file"
              accept="image/png,image/jpeg,image/webp"
              onChange={(event) => onBackgroundChange(event.target.files?.[0] || null)}
            />
          </label>
          <button type="submit">添加账号</button>
        </form>
      </nav>
      <div className="aside-foot">
        每个账号使用一张固定背景图；每个项目独立管理文案、配音、字幕和剪映草稿。
      </div>
    </aside>
  );
}
