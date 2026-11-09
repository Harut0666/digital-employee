# 选择文件夹对话框（Windows PowerShell 5.1，必须 -STA）。
# 优先用现代的 IFileOpenDialog（FOS_PICKFOLDERS：地址栏、搜索、可直接粘贴路径、可新建文件夹）；
# 任何一步出错（比如 Add-Type 编译失败）就退回 FolderBrowserDialog。
# 输出只有一行 ASCII：选中 "PATH:<UTF-8 路径的 base64>"，取消 "CANCEL"；出错写 stderr 并以 1 退出。
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms

# 隐藏的置顶属主窗口：对话框归它所有，所以总是显示在最前面，不会弹到网页或应用窗口后面
$owner = New-Object System.Windows.Forms.Form
$owner.TopMost = $true
$owner.ShowInTaskbar = $false
$owner.FormBorderStyle = 'None'
$owner.StartPosition = 'Manual'
$owner.Location = New-Object System.Drawing.Point(-32000, -32000)
$owner.Size = New-Object System.Drawing.Size(1, 1)
$owner.Show()
$owner.Activate()

function Send-Result([string]$path) {
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($path)
    [Console]::Out.Write('PATH:' + [Convert]::ToBase64String($bytes) + "`n")
}

$modern = $false
try {
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;

namespace DePick {
    [ComImport, Guid("DC1C5A9C-E88A-4DDE-A5A1-60F82A20AEF7")]
    public class FileOpenDialogClass { }

    [ComImport, Guid("43826D1E-E718-42EE-BC55-A1E261C37BFE"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    public interface IShellItem {
        void BindToHandler(IntPtr pbc, ref Guid bhid, ref Guid riid, out IntPtr ppv);
        void GetParent(out IShellItem ppsi);
        void GetDisplayName(uint sigdnName, [MarshalAs(UnmanagedType.LPWStr)] out string ppszName);
        void GetAttributes(uint sfgaoMask, out uint psfgaoAttribs);
        void Compare(IShellItem psi, uint hint, out int piOrder);
    }

    // IModalWindow.Show 之后接 IFileDialog 的方法，顺序必须和 shobjidl.h 一致
    [ComImport, Guid("42F85136-DB7E-439C-85F1-E4075D135FC8"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    public interface IFileDialog {
        [PreserveSig] int Show(IntPtr parent);
        void SetFileTypes(uint cFileTypes, IntPtr rgFilterSpec);
        void SetFileTypeIndex(uint iFileType);
        void GetFileTypeIndex(out uint piFileType);
        void Advise(IntPtr pfde, out uint pdwCookie);
        void Unadvise(uint dwCookie);
        void SetOptions(uint fos);
        void GetOptions(out uint pfos);
        void SetDefaultFolder(IShellItem psi);
        void SetFolder(IShellItem psi);
        void GetFolder(out IShellItem ppsi);
        void GetCurrentSelection(out IShellItem ppsi);
        void SetFileName([MarshalAs(UnmanagedType.LPWStr)] string pszName);
        void GetFileName([MarshalAs(UnmanagedType.LPWStr)] out string pszName);
        void SetTitle([MarshalAs(UnmanagedType.LPWStr)] string pszTitle);
        void SetOkButtonLabel([MarshalAs(UnmanagedType.LPWStr)] string pszText);
        void SetFileNameLabel([MarshalAs(UnmanagedType.LPWStr)] string pszLabel);
        void GetResult(out IShellItem ppsi);
    }

    public static class Picker {
        const uint FOS_NOCHANGEDIR = 0x8;
        const uint FOS_PICKFOLDERS = 0x20;
        const uint FOS_FORCEFILESYSTEM = 0x40;
        const uint FOS_PATHMUSTEXIST = 0x800;
        const uint SIGDN_FILESYSPATH = 0x80058000;
        const int HRESULT_CANCELLED = unchecked((int)0x800704C7);

        // 返回所选目录；用户取消返回 null
        public static string Pick(IntPtr owner, string title) {
            IFileDialog dialog = (IFileDialog)new FileOpenDialogClass();
            try {
                uint options;
                dialog.GetOptions(out options);
                dialog.SetOptions(options | FOS_PICKFOLDERS | FOS_FORCEFILESYSTEM | FOS_PATHMUSTEXIST | FOS_NOCHANGEDIR);
                dialog.SetTitle(title);
                int hr = dialog.Show(owner);
                if (hr == HRESULT_CANCELLED) return null;
                if (hr < 0) Marshal.ThrowExceptionForHR(hr);
                IShellItem item;
                dialog.GetResult(out item);
                try {
                    string path;
                    item.GetDisplayName(SIGDN_FILESYSPATH, out path);
                    return path;
                } finally {
                    Marshal.ReleaseComObject(item);
                }
            } finally {
                Marshal.ReleaseComObject(dialog);
            }
        }
    }
}
'@
    $modern = $true
} catch {
    $modern = $false
}

try {
    $path = $null
    $picked = $false
    if ($modern) {
        try {
            $path = [DePick.Picker]::Pick($owner.Handle, '选择工作区文件夹')
            $picked = $true
        } catch {
            # 现代对话框打不开：退回传统对话框
            $picked = $false
        }
    }
    if (-not $picked) {
        $legacy = New-Object System.Windows.Forms.FolderBrowserDialog
        $legacy.Description = '选择工作区文件夹'
        $legacy.ShowNewFolderButton = $true
        if ($legacy.ShowDialog($owner) -eq [System.Windows.Forms.DialogResult]::OK) {
            $path = $legacy.SelectedPath
        } else {
            $path = $null
        }
    }
    if ([string]::IsNullOrEmpty($path)) {
        [Console]::Out.Write("CANCEL`n")
    } else {
        Send-Result $path
    }
} finally {
    $owner.Close()
    $owner.Dispose()
}
