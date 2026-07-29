import { app, Menu, type MenuItemConstructorOptions } from 'electron';

export function installAppMenu(): void {
  const isMac = process.platform === 'darwin';

  if (isMac) {
    const macTemplate: MenuItemConstructorOptions[] = [
      {
        label: app.name,
        submenu: [
          { role: 'about' },
          { type: 'separator' },
          { role: 'services' },
          { type: 'separator' },
          { role: 'hide' },
          { role: 'hideOthers' },
          { role: 'unhide' },
          { type: 'separator' },
          { role: 'quit' },
        ],
      },
    ];
    Menu.setApplicationMenu(Menu.buildFromTemplate(macTemplate));
    return;
  }

  Menu.setApplicationMenu(null);
}
