# Capturar o ponteiro do Eden

Este utilitário grava um vídeo do desktop e um CSV dos pacotes DSU publicados pelo Pad2Go, com relógios alinhados. Ele é um cliente DSU: **não abre nem lê o HID do DualSense**, então o Pad2Go continua sendo o único processo que lê o controle.

## Preparar

1. No Pad2Go, ative **Enviar movimento / DSU** e **Touchpad do DualSense**.
2. No Eden, configure **Jogador 1 > Movimento 1** como `cemuhookudp`, porta `26760` e GUID `0000000000000000000000007f000001`.
3. Abra o seletor de arquivos do Mario Galaxy, onde a mão com estrela fica visível. Feche ou minimize janelas com conteúdo privado: o vídeo grava a tela inteira e inclui o cursor do Windows.
4. Tenha o FFmpeg instalado e acessível como `ffmpeg` no PATH. No Windows, precisa incluir o grabber `gdigrab`.

## Gravar

Na raiz do repositório, rode:

```powershell
go run ./cmd/touchpad-capture -duration 60s -out "$env:TEMP/eden-pointer-check"
```

Quando aparecer `Gravando desktop e DSU`, faça um swipe horizontal, levante o dedo, depois faça um swipe vertical. Deixe alguns instantes entre os gestos e mantenha cada um o mais reto possível. `Ctrl+C` encerra antes do limite; o vídeo é finalizado corretamente.

O utilitário cria dois arquivos:

- `eden-pointer-check.mp4`: vídeo da tela, incluindo a mão do jogo e o cursor do Windows.
- `eden-pointer-check.csv`: amostras dos pacotes DSU recebidos, incluindo os dois contatos do touchpad, coordenadas nativas e giroscópio publicado.

Envie ambos. O CSV identifica o deslocamento real do dedo; o vídeo permite localizar a mão do Galaxy no início e no fim de cada swipe. Com isso dá para calcular o ganho em pixels por coordenada do touchpad e o deslocamento cruzado sem você medir pontos manualmente. Se o CSV não tiver linhas além do cabeçalho, confirme que o Pad2Go mostra uma inscrição DSU ativa no Eden.

## Testes

```powershell
go test ./cmd/touchpad-capture
```

