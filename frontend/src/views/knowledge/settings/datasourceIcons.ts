import feishuIcon from '@/assets/img/datasource-feishu.ico'
import larkIcon from '@/assets/img/datasource-lark.svg'

export const datasourceIconMap: Record<string, string> = {
  feishu: feishuIcon, lark: larkIcon, feishu_drive: feishuIcon, lark_drive: larkIcon,
}

export function getDatasourceIconUrl(type: string): string | undefined {
  return datasourceIconMap[type]
}
