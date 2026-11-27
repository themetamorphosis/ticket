import {
  Alert02Icon,
  Calendar03Icon,
  CancelCircleIcon,
  CheckmarkCircle02Icon,
  Clock01Icon,
  DashboardSquare01Icon,
  InboxIcon,
  Invoice01Icon,
  Logout01Icon,
  Menu01Icon,
  QrCode01Icon,
  Search01Icon,
  UserGroupIcon,
  Wallet01Icon,
  WifiDisconnected01Icon,
} from '@hugeicons/core-free-icons';
import { Icon } from './Icon';

// Hugeicons under their Material icon names, so code reads like MUI:
//   import { Menu as MenuIcon } from './icons';
// A missing name is added here; @mui/icons-material is not installed.
function named(icon) {
  return function HugeIcon(props) {
    return <Icon icon={icon} {...props} />;
  };
}

export const AccountBalance = named(Wallet01Icon);
export const Cancel = named(CancelCircleIcon);
export const CheckCircle = named(CheckmarkCircle02Icon);
export const Dashboard = named(DashboardSquare01Icon);
export const Event = named(Calendar03Icon);
export const Groups = named(UserGroupIcon);
export const History = named(Clock01Icon);
export const Inbox = named(InboxIcon);
export const Logout = named(Logout01Icon);
export const Menu = named(Menu01Icon);
export const QrCodeScanner = named(QrCode01Icon);
export const ReceiptLong = named(Invoice01Icon);
export const Search = named(Search01Icon);
export const Warning = named(Alert02Icon);
export const WifiOff = named(WifiDisconnected01Icon);
