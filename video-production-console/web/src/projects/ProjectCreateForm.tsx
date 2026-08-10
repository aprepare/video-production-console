import type { FormEvent } from "react";

type Props = {
  accountSelected: boolean;
  title: string;
  onTitleChange: (value: string) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
};

export function ProjectCreateForm({ accountSelected, title, onTitleChange, onSubmit }: Props) {
  return (
    <form onSubmit={onSubmit} className="new-project">
      <label htmlFor="new-project-title">项目标题</label>
      <input
        id="new-project-title"
        value={title}
        onChange={(event) => onTitleChange(event.target.value)}
        placeholder={accountSelected ? "新建项目标题" : "先选择账号"}
      />
      <button disabled={!accountSelected}>新建项目</button>
    </form>
  );
}
