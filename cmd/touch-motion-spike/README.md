# Spike: touchpad do DualSense no cursor do Eden

Harness de validação, fora do caminho de execução do produto. A conversão usa o mesmo `app.TouchpadPointerState` da aplicação.

## Resultado

O usuário confirmou que o cursor do Galaxy passou a responder depois de configurar **Movimento 1** no Eden como `cemuhookudp`. O perfil original tinha o servidor DSU listado, mas continuava usando `engine:sdl`. A mão com estrela apareceu na tela de seleção de arquivo; a escala exata e o limite de 5% entre eixos ainda precisam de medidas reproduzíveis. Veja [VALIDATION.md](VALIDATION.md) para o histórico e os limites da validação.

Foram explorados três caminhos:

- **Touchpad → giroscópio DSU:** opção implementada no Pad2Go e desligada por padrão. Movimento relativo, reancora ao recolocar o dedo e volta ao giroscópio físico ao soltar. Não injeta mouse no Windows nem limita a velocidade, para preservar o deslocamento normalizado em gestos rápidos.
- **Touchpad → touchscreen DSU:** o Pad2Go já transmite os contatos e o Eden os decodifica. O teste de UDP confirma que o contato chega no pacote. Isso representa a tela de toque do Switch; não demonstra que substitua o ponteiro por movimento no jogo em modo TV. Deve ser experimentado em modo portátil, com mapeamento e calibração de touch no Eden.
- **Touchpad → mouse do sistema → movimento no Eden:** o código do Eden suporta mouse como fonte de movimento. Sem panning, depende de botão pressionado e deslocamento desde o início do arrasto; com panning usa outra escala. Não implementado: acrescentaria injeção de mouse, foco e comportamento de arrasto à investigação. DSU permite testar a hipótese com menos variáveis.

## Executar

Na raiz do repositório:

```powershell
go test ./cmd/touch-motion-spike ./internal/dsu ./internal/dualsense
go run ./cmd/touch-motion-spike -list
go run ./cmd/touch-motion-spike -duration 30s
go run ./cmd/touch-motion-spike -live -duration 2m
```

Sem `-live`, gera um arrasto horizontal e volta, depois solta. Com `-live`, lê o primeiro DualSense visível, USB ou Bluetooth. O processo termina automaticamente e fecha HID/UDP. Não cria um controle virtual; os botões continuam vindo da configuração habitual do Eden.

## Configuração do Eden para usar o DSU do Pad2Go

No Pad2Go, abra **Movimento**, ligue **Enviar movimento / DSU** e depois **Touchpad do DualSense**. O endereço padrão é `127.0.0.1:26760`.

No Eden, abra **Emulação > Configurar > Controlos > Jogador 1** e configure **Movimento 1** para `cemuhookudp`. A configuração de exemplo do Eden usa:

```ini
player_0_motionleft="motion:0,pad:0,port:26760,guid:0000000000000000000000007f000001,engine:cemuhookudp"
```

Ter `127.0.0.1:26760` listado em **Servidores UDP de entrada** não seleciona essa origem de movimento. O Eden precisa usar `cemuhookudp` em Movimento 1; com `engine:sdl`, ele continua lendo o sensor local do controle e não o DSU do Pad2Go. Em testes, a configuração antiga do Eden ainda apontava para SDL, embora o servidor UDP estivesse listado.

Se o touchpad estiver movendo o cursor do Windows, há outra configuração ativa no caminho de mouse. Desative no Steam Input o mapeamento do touchpad como mouse no **layout de desktop** ou no layout associado ao Eden; esse mapeamento não envia o gesto ao DSU. Mantenha os botões do jogo no dispositivo habitual e confira que o contador de clientes do DSU no Pad2Go mostra uma inscrição ativa.

Para testar o spike isoladamente, adicionar `127.0.0.1:26761` aos servidores UDP do Eden e associar **Movimento 1** à fonte UDP do spike, arrastando o dedo durante a captura. Se a configuração de controle UDP completo estiver ativa, evitar substituir os botões pelo pad do spike, que transmite somente touch e movimento. A porta 26761 evita disputar a porta 26760 do Pad2Go.

Verificar no Galaxy: horizontal e vertical, levantar e recolocar o dedo em outra posição, manter o dedo parado, desconectar o controle, recenter do jogo e movimentos longos. Registrar versão do Eden, edição do jogo, modo portátil/TV e fonte de movimento selecionada. Se algum eixo estiver invertido, ajustar o sinal no spike; sinais atuais são hipóteses baseadas no driver DSU do Eden, ainda sem validação visual.

## Evidência

- Testes: ângulo integrado igual em 60/125/250 Hz; escala preservada em swipe rápido e após pausa; release/recontato/troca de ID/repouso; direção vertical e diagonal; relatórios USB e Bluetooth → contato → giroscópio → pacote DSU de 100 bytes com CRC válido e contato preservado.
- Caso de transporte: dx=100 em 10 ms produz yaw=+468.75 graus/s; o código atual do Eden converte isso para gyro_z=-1.502404. Esse teste verifica o protocolo, não a integração/fusão de movimento do Eden.
- Hardware: um DualSense Bluetooth encontrado; em 5 segundos foram recebidos 1415 relatórios decodificados e publicados 625 pacotes/amostras, sem assinantes UDP. Nenhum contato ativo foi observado. Portanto leitura Bluetooth confirmada, gesto físico e jogo pendentes.

## Limites e integração proposta

A escala padrão aplica 90 graus por largura e 60 graus por altura para corresponder a 1:1 normalizado. O modo de touch emite aceleração zero, como o caminho de movimento do mouse no Eden, e não mistura giro físico com sintético. O teste do jogo ainda precisa verificar a escala real e o limite de movimento cruzado.

Se o teste no jogo passar, integrar como modo opt-in por jogador: `Original` / `Touchpad como movimento`, sensibilidade, inversão de eixos e recenter por botão. Aplicar antes de alimentar DSU e saída virtual, mantendo a mesma fonte escolhida nas duas saídas. Consumir touch no modo de movimento para evitar ativar simultaneamente a tela de toque. Zerar movimento em timeout/desconexão. A saída virtual Switch/DualSense precisa de validação própria; não foi exercitada neste spike.

## Fontes primárias

- [Driver UDP do Eden](https://github.com/eden-emulator/mirror/blob/master/src/input_common/drivers/udp_client.cpp): `OnPadData`, escala 1/312 e rearranjo dos eixos, contatos de touch e calibração.
- [Driver mouse do Eden](https://github.com/eden-emulator/mirror/blob/master/src/input_common/drivers/mouse.cpp): `UpdateMotionInput`, `Move`, `ReleaseButton` e panning.
- [Mapeamento do Eden](https://github.com/eden-emulator/mirror/blob/master/src/input_common/input_mapping.cpp): `RegisterMotion` e filtro que permite movimento UDP sem controle UDP completo.
- Código local: `internal/dualsense/parse.go`, `internal/dualsense/bluetooth.go`, `internal/app/app.go:publishMotion`, `internal/dsu/server.go`.

Os links apontam para master consultado nesta data; versões instaladas podem diferir.
