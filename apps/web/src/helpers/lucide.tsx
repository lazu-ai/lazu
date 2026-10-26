import {
  LayoutDashboard,
  TerminalSquare,
  MessageSquare,
  Key,
  BarChart3,
  Image as ImageIcon,
  CheckSquare,
  CreditCard,
  Receipt,
  Layers,
  Tag,
  User,
  Users,
  Settings,
  CircleUser,
  Package,
  Trophy,
  CalendarCheck,
  FileText,
  Activity,
  Building2,
  Megaphone,
  Telescope,
  Search,
  SlidersHorizontal,
} from 'lucide-react';
import React from 'react';

// 获取侧边栏 Lucide 图标组件
export function getLucideIcon(
  key: string,
  selected = false,
): React.ReactElement {
  const size = 16;
  const strokeWidth = 2;
  const iconColor = 'currentColor';
  const commonProps = {
    size,
    strokeWidth,
    className: `transition-colors duration-200 ${selected ? 'opacity-100' : ''}`,
  };

  switch (key) {
    case 'detail':
      return <LayoutDashboard {...commonProps} color={iconColor} />;
    case 'playground':
      return <TerminalSquare {...commonProps} color={iconColor} />;
    case 'chat':
      return <MessageSquare {...commonProps} color={iconColor} />;
    case 'token':
      return <Key {...commonProps} color={iconColor} />;
    case 'key_inspector':
      return <Key {...commonProps} color={iconColor} />;
    case 'files':
      return <FileText {...commonProps} color={iconColor} />;
    case 'image':
      return <ImageIcon {...commonProps} color={iconColor} />;
    case 'log':
    case 'usage':
      return <BarChart3 {...commonProps} color={iconColor} />;
    case 'model_ranking':
      return <Trophy {...commonProps} color={iconColor} />;
    case 'task':
      return <CheckSquare {...commonProps} color={iconColor} />;
    case 'topup':
    case 'wallet':
      return <CreditCard {...commonProps} color={iconColor} />;
    case 'billing':
      return <Receipt {...commonProps} color={iconColor} />;
    case 'monthly_report':
      return <CalendarCheck {...commonProps} color={iconColor} />;
    case 'channel':
      return <Layers {...commonProps} color={iconColor} />;
    /* 'user' is the admin 用户 page (organizations). It sits in the same column
       as 个人设置, so it needs its own glyph rather than a second portrait. */
    case 'user':
      return <Building2 {...commonProps} color={iconColor} />;
    case 'personal':
      return <User {...commonProps} color={iconColor} />;
    case 'model_market':
      return <Package {...commonProps} color={iconColor} />;
    /* 模型运营 is the control layer over the same objects the workspace 模型 page
       displays — same family of icon, different weight. */
    case 'models':
    case 'model_ops':
      return <Layers {...commonProps} color={iconColor} />;
    case 'pricing':
      return <Tag {...commonProps} color={iconColor} />;
    case 'setting':
      return <Settings {...commonProps} color={iconColor} />;
    case 'search_backend':
      return <Telescope {...commonProps} color={iconColor} />;
    case 'search':
      return <Search {...commonProps} color={iconColor} />;
    case 'channel_health':
      return <Activity {...commonProps} color={iconColor} />;
    case 'project_members':
      return <Users {...commonProps} color={iconColor} />;
    case 'project_settings':
      return <SlidersHorizontal {...commonProps} color={iconColor} />;
    case 'status':
    case 'announcements':
      return <Megaphone {...commonProps} color={iconColor} />;
    default:
      return <CircleUser {...commonProps} color={iconColor} />;
  }
}
