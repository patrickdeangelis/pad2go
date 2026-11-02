# Validação executada no Eden

## Reavaliação da execução mais recente (2026-11-01)

O resultado acima não deve ser tratado como validação ponta a ponta: a interação observada pode ter sido o touchpad atuando como mouse via Steam/Windows, e não o ponteiro do Galaxy recebendo movimento DSU do Pad2Go. O usuário confirmou que o touch controlava o mouse e que o Eden não reconheceu o toque do controle. A inspeção da interface foi interrompida pelo usuário antes de uma medição reproduzível; nenhum movimento vertical ou limite de 5% foi comprovado nesta execução.

Uma leitura Bluetooth posterior do spike recebeu 2459 relatórios, mas zero relatórios com contato ativo (`touch_reports=0`). Portanto, ela confirma acesso à entrada HID, mas não prova que o toque físico chegou ao Pad2Go. O arquivo de configuração do Eden foi restaurado após o teste (SHA-256 confere com o backup). A tela/Steam Input precisa ser configurada para não converter o touchpad em mouse durante uma repetição, e o teste deve confirmar separadamente o contato em Pad2Go e o movimento no Eden antes de atribuir sucesso ao DSU.

Na repetição seguinte, encontrei o perfil salvo do Eden com `udp_input_servers=127.0.0.1:26760`, mas `player_0_motionleft/right` ainda apontando para `engine:sdl`. Troquei temporariamente ambos para o mapeamento CemuHookUDP documentado e associei A ao teclado para tentar abrir o seletor. O jogo carregou até o título, mas a entrada A não avançou; nenhum deslocamento nem inscrição DSU foi observado nesse estado. A captura HID seguinte recebeu 3486 relatórios Bluetooth e novamente zero contatos ativos. Não foi possível chegar ao seletor nem medir os eixos. O perfil original do Eden foi restaurado, incluindo as fontes SDL, e o hash do arquivo voltou a `7E5CEC36745406990A35258E0C25D1FBF8BAC283D81ED96D02C6F59B1768A4D4`.

Este adendo substitui o veredito de sucesso no jogo abaixo. Os resultados antigos ficam registrados como observações não conclusivas.

2026-11-01. Eden v0.2.1 / Clang 22.1.4, Galaxy 1.0.0 (010099C022B96000), modo TV, Vulkan, aproximadamente 60 FPS. Teste no menu de seleção de arquivos; nenhum arquivo de save foi aberto ou criado.

## Veredito

**O caminho DSU funciona dentro do jogo. O conversor simples ainda não está pronto para integração.** A mão com estrela do Galaxy respondeu a contatos sintéticos processados pelo conversor do spike. O mouse do Windows não foi usado para mover essa mão. A leitura do DualSense Bluetooth já havia sido comprovada separadamente; nesta sessão os gestos foram injetados por arquivo para permitir testes sem intervenção física.

## Procedimento e observações

1. Backup de `qt-config.ini` em `%TEMP%/eden-before-touch-spike.ini`. Aplicação fechada ao editar o arquivo.
2. Fonte de Movimento 1/2 temporária: `engine:cemuhookudp,motion:0,port:26760,pad:0,guid:0000000000000000000000007f000001`. Botões A/B/+/R temporariamente acessíveis pelo teclado. O Eden assinou o servidor UDP do spike (registro confirmado no log do servidor).
3. Acesso ao menu de seleção de arquivos, onde aparece a mão com estrela. A tela inicial de título não exibe esse ponteiro.
4. Arrasto horizontal X=500→900, Y=500, ganho 90: mão chegou à borda esquerda. Retorno X=900→500 deslocou a mão de volta à região direita. Confirma resposta aos dois sentidos, mas orientação/escala inadequadas.
5. Arrasto vertical Y=500→600: mão foi para a borda inferior. Ganho reduzido para 30; novo processo do spike; Y=500→200 fez a mão subir e destacar o arquivo 4. Ganho 30 é candidato, não sensibilidade final validada.
6. Mudança experimental do sinal horizontal e X=500→600, Y=200: houve deslocamento com componente vertical indesejada. **Reprovado como controle de dois eixos independente.** Trocar apenas o sinal não resolve. A hipótese é interação da orientação acumulada com aceleração constante/fusão do Eden; ainda não foi isolada nem comprovada.
7. Repouso: a mão estabilizou aproximadamente na região observada. Não houve medição de deriva longa ou de latência.
8. Release `0 600 200`, depois contato `1 1600 800`: a mão permaneceu na mesma região, sem salto para a nova posição absoluta. Reancoragem relativa confirmada visualmente.

As posições são observações visuais, não medidas automatizadas de pixels. Não foi validada uma fase jogável, coleta de Star Bits, uso prolongado, click/remap do touchpad ou gesto físico de uma pessoa. O teste valida a viabilidade de transporte e a resposta do ponteiro, não a qualidade final da experiência.

## Repetir com gestos determinísticos

```powershell
Set-Content "$env:TEMP/touch-gesture.txt" '0 500 500'
go run ./cmd/touch-motion-spike -addr 127.0.0.1:26760 -duration 5m -gesture-file "$env:TEMP/touch-gesture.txt"
```

Outro terminal altera o arquivo para `1 X Y` (contato) ou `0 X Y` (solto). Para arrastar, variar uma coordenada gradualmente. Esse modo não modifica o jogo nem seu save. Evitar truncamentos longos durante a escrita: arquivos inválidos são tratados como dedo solto.

## Próximo experimento indicado

Comparar giroscópio sintético com aceleração zero contra aceleração coerente com uma orientação virtual, começando cada cenário com orientação reiniciada. Verificar que horizontal não altera vertical e vice-versa, e testar o recenter do jogo. O passo de integração só deve ocorrer após esses critérios passarem. Os sinais atuais do código continuam experimentais.

## Reavaliação do Eden com Pad2Go (2026-11-01)

O perfil original do Eden tinha o servidor DSU em `127.0.0.1:26760`, mas os campos `player_0_motionleft` e `player_0_motionright` apontavam para `engine:sdl`. Isso fazia o Eden continuar usando o giroscópio local mesmo com o endereço UDP cadastrado. Troquei temporariamente a origem de Movimento 1/2 para `engine:cemuhookudp` na porta 26760; o log do Pad2Go confirmou `DSU client subscribed`.

Com um config YAML temporário do Pad2Go, validei o estado da interface desligado/ligado, o texto de pré-requisito, persistência de `gyro_passthrough_mode: Cemuhook` e `touchpad_pointer_enabled: true` após fechar e reabrir o app. No Eden, o Galaxy chegou à tela “Please choose a file”; após o usuário confirmar que passou a funcionar, a mão com estrela estava visível sobre o slot 5. Isso confirma que a rota DSU do Eden estava configurada e que o cursor do jogo apareceu; não mede o deslocamento exato de um gesto.

Uma captura `-live` simultânea ao Pad2Go recebeu zero contatos ativos e, depois, a interface do Pad2Go mostrou relatórios inválidos. Como dois processos tentaram ler o mesmo HID, essa captura não é evidência confiável do comportamento do contato físico. Os critérios de 25% por eixo e no máximo 5% de movimento cruzado continuam pendentes de uma repetição sem um segundo leitor HID. O `qt-config.ini` foi restaurado exatamente ao backup original: SHA-256 `7E5CEC36745406990A35258E0C25D1FBF8BAC283D81ED96D02C6F59B1768A4D4`.