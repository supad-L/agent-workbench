import { Button, Tag } from 'antd';
import { ApiOutlined, SafetyCertificateOutlined, UserOutlined } from '@ant-design/icons';

export type AccountProvider = 'codex' | 'claude';

type AccessProps = {
  selected: 'api' | AccountProvider;
  onAPI?: () => void;
  onAccount?: (provider: AccountProvider) => void;
  disabled?: boolean;
};

export function ConnectionAccess({ selected, onAPI, onAccount, disabled }: AccessProps) {
  const options = [
    { id: 'api' as const, title: 'API / Coding Plan', icon: <ApiOutlined aria-hidden="true" />,
      detail: '公司中转、自定义服务或套餐专用 API Key。', action: onAPI, label: '配置 API 连接' },
    { id: 'codex' as const, title: 'GPT 官方账号', icon: <UserOutlined aria-hidden="true" />,
      detail: '通过官方 Codex 工具登录，单独查看授权与执行条件。', action: onAccount ? () => onAccount('codex') : undefined, label: '管理 GPT 账号' },
    { id: 'claude' as const, title: 'Claude 官方账号', icon: <SafetyCertificateOutlined aria-hidden="true" />,
      detail: '先检查专用出口条件，满足隔离要求后开放登录。', action: onAccount ? () => onAccount('claude') : undefined, label: '查看 Claude 接入条件' },
  ];
  return <nav className="connection-access" aria-label="选择接入方式">
    {options.map(option => <article key={option.id} className={`access-option${selected === option.id ? ' is-selected' : ''}`}>
      <div className="access-identity">{option.icon}<h2>{option.title}</h2>{selected === option.id && <Tag color="blue">当前方式</Tag>}</div>
      <p>{option.detail}</p>
      <Button type="link" disabled={disabled || !option.action} onClick={option.action}>{option.label}</Button>
    </article>)}
  </nav>;
}

export function ConnectionProgress({ saved, dirty, checked, tested, onRoles }: {
  saved: boolean; dirty: boolean; checked: boolean; tested: boolean; onRoles?: () => void;
}) {
  return <section className="connection-progress" aria-label="连接配置与验证状态">
    <div className="connection-progress-heading"><h3>配置与验证</h3><p>可先保存连接；工具验证会消耗 API 额度。</p></div>
    <ol>
      <li><strong>保存配置</strong><span>{saved && !dirty ? '已加密保存' : saved ? '有未保存修改' : '尚未保存'}</span></li>
      <li><strong>检查格式</strong><span>{checked || tested ? '本次配置格式有效' : '尚未检查当前配置'}</span></li>
      <li><strong>验证工具回合</strong><span>{tested ? '本次工具验证通过' : '当前配置尚未验证'}</span></li>
      <li><strong>绑定角色</strong><Button size="small" disabled={!saved || dirty || !onRoles} onClick={onRoles}>前往配置角色</Button></li>
    </ol>
    <small>检查和验证结果仅对应本次表单；修改配置后需重新核对。已保存不代表模型已连通。</small>
  </section>;
}
