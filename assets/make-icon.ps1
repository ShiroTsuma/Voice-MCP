# Renders voice.ico (and voice-muted.ico for the tray) with System.Drawing:
# the settings window's dark ground, light rising from below, three bars. Run after changing the design;
# build.ps1 compiles voice.rc with the result into the executable.
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Drawing

function New-IconPng([int]$size, [bool]$muted) {
    # The settings window's look: its dark ground, light rising from below as
    # it rises from whoever speaks, and three bars of a voice in its ink.
    $bmp = New-Object Drawing.Bitmap $size, $size, ([Drawing.Imaging.PixelFormat]::Format32bppArgb)
    $g = [Drawing.Graphics]::FromImage($bmp)
    $g.SmoothingMode = 'AntiAlias'
    $g.Clear([Drawing.Color]::Transparent)
    $r = [math]::Max(2, $size * 0.22)
    $rect = New-Object Drawing.RectangleF ($size * 0.04), ($size * 0.04), ($size * 0.92), ($size * 0.92)
    $path = New-Object Drawing.Drawing2D.GraphicsPath
    $d = $r * 2
    $path.AddArc($rect.X, $rect.Y, $d, $d, 180, 90)
    $path.AddArc($rect.Right - $d, $rect.Y, $d, $d, 270, 90)
    $path.AddArc($rect.Right - $d, $rect.Bottom - $d, $d, $d, 0, 90)
    $path.AddArc($rect.X, $rect.Bottom - $d, $d, $d, 90, 90)
    $path.CloseFigure()
    $g.FillPath((New-Object Drawing.SolidBrush ([Drawing.Color]::FromArgb(255, 31, 32, 35))), $path)

    if (-not $muted) {
        # The light: a soft pool in the first character colour, from the bottom edge.
        $g.SetClip($path)
        $glow = New-Object Drawing.Drawing2D.GraphicsPath
        $glow.AddEllipse(($size * -0.25), ($size * 0.42), ($size * 1.5), ($size * 1.1))
        $pb = New-Object Drawing.Drawing2D.PathGradientBrush $glow
        $pb.CenterPoint = New-Object Drawing.PointF ($size * 0.5), ($size * 1.02)
        $pb.CenterColor = [Drawing.Color]::FromArgb(200, 112, 201, 148)
        $pb.SurroundColors = @([Drawing.Color]::FromArgb(0, 112, 201, 148))
        $g.FillPath($pb, $glow)
        $g.ResetClip()
    }

    # A voice: three rounded bars, uneven as speech is.
    $heights = 0.30, 0.56, 0.40
    $barW = $size * 0.13
    $gap = $size * 0.085
    $total = 3 * $barW + 2 * $gap
    $x = ($size - $total) / 2
    $ink = if ($muted) { [Drawing.Color]::FromArgb(255, 128, 131, 137) } else { [Drawing.Color]::FromArgb(255, 236, 236, 234) }
    $brush = New-Object Drawing.SolidBrush $ink
    foreach ($h in $heights) {
        $bh = $size * $h
        $y = ($size - $bh) / 2 - $size * 0.03
        $bar = New-Object Drawing.Drawing2D.GraphicsPath
        $bw = [math]::Max(1.0, $barW)
        $bar.AddArc($x, $y, $bw, $bw, 180, 180)
        $bar.AddArc($x, $y + $bh - $bw, $bw, $bw, 0, 180)
        $bar.CloseFigure()
        $g.FillPath($brush, $bar)
        $x += $barW + $gap
    }
    if ($muted) {
        # Muted: a slash in the ink, cut out of the ground so it reads at 16 px.
        $cut = New-Object Drawing.Pen ([Drawing.Color]::FromArgb(255, 31, 32, 35)), ([math]::Max(3.0, $size * 0.2))
        $cut.StartCap = 'Round'; $cut.EndCap = 'Round'
        $g.DrawLine($cut, $size * 0.22, $size * 0.78, $size * 0.78, $size * 0.22)
        $pen = New-Object Drawing.Pen ([Drawing.Color]::FromArgb(255, 218, 219, 222)), ([math]::Max(1.5, $size * 0.09))
        $pen.StartCap = 'Round'; $pen.EndCap = 'Round'
        $g.DrawLine($pen, $size * 0.22, $size * 0.78, $size * 0.78, $size * 0.22)
    }
    $g.Dispose()
    $ms = New-Object IO.MemoryStream
    $bmp.Save($ms, [Drawing.Imaging.ImageFormat]::Png)
    $bmp.Dispose()
    return , $ms.ToArray()
}
function Write-Ico([string]$path, [bool]$muted) {
    $sizes = 16, 20, 24, 32, 40, 48, 64, 256
    $images = foreach ($s in $sizes) { , (New-IconPng $s $muted) }
    $out = New-Object IO.MemoryStream
    $w = New-Object IO.BinaryWriter $out
    $w.Write([uint16]0); $w.Write([uint16]1); $w.Write([uint16]$sizes.Count)
    $offset = 6 + 16 * $sizes.Count
    for ($i = 0; $i -lt $sizes.Count; $i++) {
        $s = $sizes[$i]
        $dim = if ($s -ge 256) { 0 } else { $s }
        $w.Write([byte]$dim); $w.Write([byte]$dim); $w.Write([byte]0); $w.Write([byte]0)
        $w.Write([uint16]1); $w.Write([uint16]32)
        $w.Write([uint32]$images[$i].Length); $w.Write([uint32]$offset)
        $offset += $images[$i].Length
    }
    foreach ($img in $images) { $w.Write($img) }
    $w.Flush()
    [IO.File]::WriteAllBytes($path, $out.ToArray())
}

# The tray embeds the icons, so they live with its package.
$iconDir = Join-Path $PSScriptRoot '..\internal\tray'
Write-Ico (Join-Path $iconDir 'voice.ico') $false
Write-Ico (Join-Path $iconDir 'voice-muted.ico') $true

# Compile the Windows resource; Go links any .syso in the package.
$windres = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..\resources\mingw64\bin\windres.exe'))
$cmd = Join-Path $PSScriptRoot '..\cmd\voice'
Push-Location $cmd
try {
    & $windres -O coff -i voice.rc -o rsrc_windows_amd64.syso
    if ($LASTEXITCODE) { throw 'windres failed' }
}
finally { Pop-Location }
Write-Host 'icons and resource written'
